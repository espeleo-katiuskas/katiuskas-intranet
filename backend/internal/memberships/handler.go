package memberships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/memberships,
// but memberships is keyed by numeric user id — users.Service satisfies
// this structurally (see main.go's wiring), without memberships having
// to import that package (it can't: users already imports this one).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — see users.Handler's own
// actorLogin, the same pattern. Falls back to a placeholder rather than
// failing the whole request if the lookup itself errors, since a
// notification is never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// Only admin can write here now — board can see every membership period
// (this whole resource is read-only to them, like the rest of a
// person's data) but not start, correct, or end one.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/memberships", h.authenticator.Protect(h.listActive, "admin", "board"))
	mux.Handle("PATCH /api/v1/memberships/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/memberships/{id}", h.authenticator.Protect(h.delete, "admin"))
	mux.Handle("GET /api/v1/users/{login}/memberships", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{login}/memberships", h.authenticator.Protect(h.create, "admin"))

	mux.Handle("GET /api/v1/users/{login}/leaves", h.authenticator.Protect(h.listLeavesByUser, "admin", "board"))
	mux.Handle("POST /api/v1/users/{login}/leaves", h.authenticator.Protect(h.createLeave, "admin"))
	mux.Handle("PATCH /api/v1/leaves/{id}", h.authenticator.Protect(h.updateLeave, "admin"))
	mux.Handle("DELETE /api/v1/leaves/{id}", h.authenticator.Protect(h.deleteLeave, "admin"))

	// Dues follow the same self-or-admin/board read, admin-only write
	// split as the rest of a person's data (licenses included) — see
	// listByUser/create above.
	mux.Handle("GET /api/v1/users/{login}/dues", h.authenticator.Protect(h.listDuesByUser))
	mux.Handle("POST /api/v1/users/{login}/dues", h.authenticator.Protect(h.createDue, "admin"))
}

func (h *Handler) listActive(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListActive(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list memberships")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// resolveUserLogin looks up the numeric id behind a {login} path
// segment. Any failure — malformed, or simply nobody by that login — is
// reported the same way (404): this package only needs to know whether
// the lookup succeeded, not why it didn't.
func (h *Handler) resolveUserLogin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	return id, true
}

func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own membership history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list memberships")
		return
	}
	for i := range list {
		list[i].UserLogin = login
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		StartDate string `json:"start_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	startDate, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	m, err := h.service.Create(r.Context(), CreateInput{
		UserID: userID, StartDate: startDate,
	})
	if errors.Is(err, ErrOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create membership")
		return
	}
	m.UserLogin = login
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha dado de alta como socio/a a %s (desde %s).",
		h.actorLogin(r.Context()), login, m.StartDate.Format("2006-01-02")))
	httpapi.WriteJSON(w, http.StatusCreated, m)
}

// update covers both ending a membership (send just end_date) and fully
// correcting a period's dates later — start_date is always required and
// end_date always replaces whatever was there (null/omitted means the
// period is open), so there's no ambiguity about which fields a partial
// body was meant to leave alone versus clear.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		StartDate string  `json:"start_date"`
		EndDate   *string `json:"end_date"`
		EndReason *string `json:"end_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	startDate, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	var endDate *time.Time
	if body.EndDate != nil && *body.EndDate != "" {
		d, err := time.Parse("2006-01-02", *body.EndDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
			return
		}
		endDate = &d
	}

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below — a failure here isn't
	// reason enough to fail the actual edit, so it's only ever
	// consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), id)

	m, err := h.service.Update(r.Context(), id, UpdateInput{
		StartDate: startDate, EndDate: endDate, EndReason: body.EndReason,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	if errors.Is(err, ErrOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if errors.Is(err, ErrLeaveOutsideMembership) {
		httpapi.WriteError(w, http.StatusConflict, "outside_membership_period",
			"this change would leave an existing temporary leave outside the membership period")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update membership")
		return
	}
	login, err := h.users.LoginForUserID(r.Context(), m.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve membership owner")
		return
	}
	m.UserLogin = login
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, m, "StartDate", "EndDate", "EndReason"); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado la membresía de %s:\n%s",
				h.actorLogin(r.Context()), login, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// Fetched before deleting, purely so the mutation notification below
	// can still name the person and date afterward — there's nothing
	// left to look up once Delete succeeds.
	target, targetErr := h.service.Get(r.Context(), id)

	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete membership")
		return
	}
	if targetErr == nil {
		if login, err := h.users.LoginForUserID(r.Context(), target.UserID); err == nil {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado un alta de %s (desde %s).",
				h.actorLogin(r.Context()), login, target.StartDate.Format("2006-01-02")))
		}
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) listLeavesByUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	list, err := h.service.ListLeavesByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list temporary leaves")
		return
	}
	for i := range list {
		list[i].UserLogin = login
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// leaveOwnerLogin resolves the login of whoever userID is, for the
// leave-related mutation notifications below (PATCH/DELETE /leaves/{id}
// aren't scoped under a known /users/{login} the way listLeavesByUser/
// createLeave are, so the login has to be looked up). Returns ""
// (silently skipped by the caller) on any failure, the same best-effort
// spirit as actorLogin: a notification detail is never worth failing an
// otherwise-successful write over.
func (h *Handler) leaveOwnerLogin(ctx context.Context, userID int64) string {
	login, err := h.users.LoginForUserID(ctx, userID)
	if err != nil {
		return ""
	}
	return login
}

func (h *Handler) createLeave(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		StartDate string  `json:"start_date"`
		EndDate   *string `json:"end_date"`
		Reason    *string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	startDate, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	endDate, err := parseOptionalDate(body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.CreateLeave(r.Context(), CreateLeaveInput{
		UserID: userID, StartDate: startDate, EndDate: endDate, Reason: body.Reason,
	})
	if errors.Is(err, ErrLeaveOutsideMembership) {
		httpapi.WriteError(w, http.StatusConflict, "outside_membership_period", err.Error())
		return
	}
	if errors.Is(err, ErrLeaveOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create temporary leave")
		return
	}
	l.UserLogin = login
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha registrado una excedencia de %s (%s).",
		h.actorLogin(r.Context()), login, leavePeriodText(l.StartDate, l.EndDate)))
	httpapi.WriteJSON(w, http.StatusCreated, l)
}

// parseOptionalDate parses a nullable YYYY-MM-DD field — nil or empty
// both mean "not given" (a leave still ongoing, no end_date yet),
// mirroring how Handler.update already treats membership's own optional
// end_date.
func parseOptionalDate(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// leavePeriodText renders a leave's own date range for a mutation
// notification — "<start> a <end>" once it's actually closed, or just
// "desde <start>" while it's still ongoing.
func leavePeriodText(start time.Time, end *time.Time) string {
	if end == nil {
		return fmt.Sprintf("desde %s, todavía en curso", start.Format("2006-01-02"))
	}
	return fmt.Sprintf("%s a %s", start.Format("2006-01-02"), end.Format("2006-01-02"))
}

func (h *Handler) updateLeave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		StartDate string  `json:"start_date"`
		EndDate   *string `json:"end_date"`
		Reason    *string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	startDate, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	endDate, err := parseOptionalDate(body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below — a failure here isn't
	// reason enough to fail the actual edit, so it's only ever
	// consulted if it succeeded.
	before, beforeErr := h.service.GetLeave(r.Context(), id)

	l, err := h.service.UpdateLeave(r.Context(), id, UpdateLeaveInput{
		StartDate: startDate, EndDate: endDate, Reason: body.Reason,
	})
	if errors.Is(err, ErrLeaveNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "temporary leave not found")
		return
	}
	if errors.Is(err, ErrLeaveOutsideMembership) {
		httpapi.WriteError(w, http.StatusConflict, "outside_membership_period", err.Error())
		return
	}
	if errors.Is(err, ErrLeaveOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update temporary leave")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, l, "StartDate", "EndDate", "Reason"); len(diff) > 0 {
			login := h.leaveOwnerLogin(r.Context(), l.UserID)
			if login == "" {
				login = fmt.Sprintf("user#%d", l.UserID)
			}
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado una excedencia de %s:\n%s",
				h.actorLogin(r.Context()), login, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) deleteLeave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// Fetched before deleting, purely so the mutation notification below
	// can still describe the leave afterward — there's nothing left to
	// look up once DeleteLeave succeeds.
	target, targetErr := h.service.GetLeave(r.Context(), id)

	err = h.service.DeleteLeave(r.Context(), id)
	if errors.Is(err, ErrLeaveNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "temporary leave not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete temporary leave")
		return
	}
	if targetErr == nil {
		login := h.leaveOwnerLogin(r.Context(), target.UserID)
		if login == "" {
			login = fmt.Sprintf("user#%d", target.UserID)
		}
		endText := "todavía en curso"
		if target.EndDate != nil {
			endText = target.EndDate.Format("2006-01-02")
		}
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado una excedencia de %s (%s a %s).",
			h.actorLogin(r.Context()), login, target.StartDate.Format("2006-01-02"), endText))
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) listDuesByUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own dues history")
		return
	}
	list, err := h.service.ListDuesForUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list dues")
		return
	}
	for i := range list {
		list[i].UserLogin = login
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// createDue records a due (see Service.RecordDue) — start_date is only
// actually used when this is the person's very first one, to start
// their membership period alongside it; correcting or removing a due
// isn't supported at all yet (see CLAUDE.md's Accounting section on
// this feature's own additive-only editing story).
func (h *Handler) createDue(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		Year          int    `json:"year"`
		TransactionID int64  `json:"transaction_id"`
		StartDate     string `json:"start_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.Year == 0 || body.TransactionID == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "year and transaction_id are both required")
		return
	}
	startDate, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}

	d, err := h.service.RecordDue(r.Context(), userID, body.Year, body.TransactionID, startDate)
	if errors.Is(err, ErrDueAlreadyRecorded) {
		httpapi.WriteError(w, http.StatusConflict, "due_already_recorded", err.Error())
		return
	}
	if errors.Is(err, ErrUnknownTransaction) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_transaction", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not record due")
		return
	}
	d.UserLogin = login
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha registrado la cuota de %d de %s.",
		h.actorLogin(r.Context()), d.Year, login))
	httpapi.WriteJSON(w, http.StatusCreated, d)
}

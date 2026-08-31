package memberships

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users}
}

// Only admin can write here now — board can see every membership period
// (this whole resource is read-only to them, like the rest of a
// person's data) but not start, correct, or end one.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/memberships", h.authenticator.Protect(h.listActive, "admin", "board"))
	mux.Handle("PATCH /api/v1/memberships/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("GET /api/v1/users/{login}/memberships", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{login}/memberships", h.authenticator.Protect(h.create, "admin"))

	mux.Handle("GET /api/v1/memberships/{membershipID}/leaves", h.authenticator.Protect(h.listLeaves, "admin", "board"))
	mux.Handle("POST /api/v1/memberships/{membershipID}/leaves", h.authenticator.Protect(h.createLeave, "admin"))
	mux.Handle("PATCH /api/v1/leaves/{id}", h.authenticator.Protect(h.updateLeave, "admin"))
	mux.Handle("DELETE /api/v1/leaves/{id}", h.authenticator.Protect(h.deleteLeave, "admin"))
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
	httpapi.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) listLeaves(w http.ResponseWriter, r *http.Request) {
	membershipID, err := strconv.ParseInt(r.PathValue("membershipID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "membership id must be numeric")
		return
	}
	list, err := h.service.ListLeavesByMembership(r.Context(), membershipID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list temporary leaves")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createLeave(w http.ResponseWriter, r *http.Request) {
	membershipID, err := strconv.ParseInt(r.PathValue("membershipID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "membership id must be numeric")
		return
	}
	var body struct {
		StartDate string  `json:"start_date"`
		EndDate   string  `json:"end_date"`
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
	endDate, err := time.Parse("2006-01-02", body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.CreateLeave(r.Context(), CreateLeaveInput{
		MembershipID: membershipID, StartDate: startDate, EndDate: endDate, Reason: body.Reason,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "membership not found")
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
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create temporary leave")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) updateLeave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		StartDate string  `json:"start_date"`
		EndDate   string  `json:"end_date"`
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
	endDate, err := time.Parse("2006-01-02", body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}

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
	httpapi.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) deleteLeave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	err = h.service.DeleteLeave(r.Context(), id)
	if errors.Is(err, ErrLeaveNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "temporary leave not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete temporary leave")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

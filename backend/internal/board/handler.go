package board

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
// This package's own routes are nested under /users/{login}/board-members,
// but board_members is keyed by numeric user id — users.Service satisfies
// this structurally (see main.go's wiring), without board having to
// import that package directly. licenses/memberships/roles genuinely
// can't (users.Service imports all three); board keeps the same small
// local-interface shape anyway for consistency across every package
// that's keyed by user id.
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
// copy of this same helper. Falls back to a placeholder rather than
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

// listActive is open to plain members too — anyone can see who's
// currently on the Junta Directiva, cargo and contact details included
// (see Member's fields) — but only admin can write here: changing who's
// on the board is data about a person, same as the rest of their
// profile, which board itself can no longer touch.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/board-members", h.authenticator.Protect(h.listActive, "admin", "board", "member"))
	mux.Handle("GET /api/v1/board-members/history", h.authenticator.Protect(h.listHistory, "admin", "board", "member"))
	mux.Handle("PUT /api/v1/board-members/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/board-members/{id}", h.authenticator.Protect(h.delete, "admin"))
	mux.Handle("GET /api/v1/users/{login}/board-members", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{login}/board-members", h.authenticator.Protect(h.create, "admin"))
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

func (h *Handler) listActive(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListActive(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list board members")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listHistory is every term there has been on the board, past and present, for whoever can see the
// current one: who served, in which position and between which dates.
func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListAll(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list the board history")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own board history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list board members")
		return
	}
	for i := range list {
		list[i].UserLogin = login
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// parseOptionalEndDate reads an end_date that may be missing, null or empty (an open term). On a
// malformed one it answers 400 itself and reports false.
func parseOptionalEndDate(w http.ResponseWriter, raw *string) (*time.Time, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	d, err := time.Parse("2006-01-02", *raw)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return nil, false
	}
	return &d, true
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		Position  Position `json:"position"`
		StartDate string   `json:"start_date"`
		EndDate   *string  `json:"end_date"`
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
	endDate, ok := parseOptionalEndDate(w, body.EndDate)
	if !ok {
		return
	}
	m, err := h.service.Create(r.Context(), CreateInput{
		UserID: userID, Position: body.Position, StartDate: startDate, EndDate: endDate,
	})
	if errors.Is(err, ErrInvalidPosition) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_position", err.Error())
		return
	}
	if errors.Is(err, ErrEndBeforeStart) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", err.Error())
		return
	}
	if errors.Is(err, ErrOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create board term")
		return
	}
	m.UserLogin = login
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha añadido a %s a la junta directiva como %s (desde %s).",
		h.actorLogin(r.Context()), login, m.Position, m.StartDate.Format("2006-01-02")))
	httpapi.WriteJSON(w, http.StatusCreated, m)
}

// update covers both ending a board term (send just end_date) and fully
// correcting one later — position and start_date are always required,
// and end_date always replaces whatever was there (null/omitted means
// the term is open), the same convention memberships.update uses.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Position  Position `json:"position"`
		StartDate string   `json:"start_date"`
		EndDate   *string  `json:"end_date"`
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
	endDate, ok := parseOptionalEndDate(w, body.EndDate)
	if !ok {
		return
	}

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below (see auditableFields)
	// — a failure here isn't reason enough to fail the actual edit, so
	// it's only ever consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), id)

	m, err := h.service.Update(r.Context(), id, UpdateInput{
		Position: body.Position, StartDate: startDate, EndDate: endDate,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "board term not found")
		return
	}
	if errors.Is(err, ErrInvalidPosition) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_position", err.Error())
		return
	}
	if errors.Is(err, ErrEndBeforeStart) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", err.Error())
		return
	}
	if errors.Is(err, ErrOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update board term")
		return
	}
	login, err := h.users.LoginForUserID(r.Context(), m.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve board term owner")
		return
	}
	m.UserLogin = login
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, m, auditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el cargo de %s en la junta directiva:\n%s",
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
	// can still name the person, cargo, and date afterward — there's
	// nothing left to look up once Delete succeeds.
	target, targetErr := h.service.Get(r.Context(), id)

	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "board term not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete board term")
		return
	}
	if targetErr == nil {
		if login, err := h.users.LoginForUserID(r.Context(), target.UserID); err == nil {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el cargo de %s de %s en la junta directiva (desde %s).",
				h.actorLogin(r.Context()), target.Position, login, target.StartDate.Format("2006-01-02")))
		}
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// auditableFields are the Member fields update's own mutation
// notification diffs (see httpapi.DiffFields) — matching UpdateInput's
// field set exactly.
var auditableFields = []string{"Position", "StartDate", "EndDate"}

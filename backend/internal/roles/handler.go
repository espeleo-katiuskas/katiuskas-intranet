package roles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup resolves a login into the numeric id it names. This
// package's role-assignment routes are nested under /users/{login}/...,
// but user_roles is keyed by that person's numeric id, so the handler
// needs this translation before it can call into Service —
// users.Service satisfies this structurally (see main.go's wiring),
// without roles having to import that package.
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

// Assigning or revoking a role is data about a person, same as their
// profile fields — only admin can touch who holds what. The catalog
// itself is just organizational labels, open to any baseline role
// (read-only) so a plain member's own "Cargo" column on /members can
// tell an automatic role (member/former_member/board) apart from one
// that's actually a distinct label to show.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/roles", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/roles", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("DELETE /api/v1/roles/{id}", h.authenticator.Protect(h.delete, "admin"))

	mux.Handle("POST /api/v1/users/{login}/roles", h.authenticator.Protect(h.assign, "admin"))
	mux.Handle("DELETE /api/v1/users/{login}/roles/{roleID}", h.authenticator.Protect(h.remove, "admin"))
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.List(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list roles")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.Name == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}

	role, err := h.service.Create(r.Context(), CreateInput{Name: body.Name, Description: body.Description})
	if errors.Is(err, ErrDuplicateName) {
		httpapi.WriteError(w, http.StatusConflict, "duplicate", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create role")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado el rol «%s».", h.actorLogin(r.Context()), role.Name))
	httpapi.WriteJSON(w, http.StatusCreated, role)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// Fetched before deleting, purely so the mutation notification below
	// can still name the role afterward — there's nothing left to look
	// up once Delete succeeds.
	target, targetErr := h.service.Get(r.Context(), id)

	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "role not found")
		return
	}
	if errors.Is(err, ErrSystemRole) {
		httpapi.WriteError(w, http.StatusConflict, "system_role", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete role")
		return
	}
	if targetErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el rol «%s».", h.actorLogin(r.Context()), target.Name))
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	var body struct {
		RoleID int64 `json:"role_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	role, err := h.service.Get(r.Context(), body.RoleID)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "role not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not look up role")
		return
	}
	if err := h.service.Assign(r.Context(), userID, role.ID); err != nil {
		if errors.Is(err, ErrAutomaticRole) {
			httpapi.WriteError(w, http.StatusConflict, "automatic_role", err.Error())
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not assign role")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha asignado el rol «%s» a %s.",
		h.actorLogin(r.Context()), role.Name, r.PathValue("login")))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	roleID, err := strconv.ParseInt(r.PathValue("roleID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "role id must be numeric")
		return
	}

	role, err := h.service.Get(r.Context(), roleID)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "role not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not look up role")
		return
	}
	err = h.service.Remove(r.Context(), userID, roleID)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "that person doesn't have this role")
		return
	}
	if errors.Is(err, ErrAutomaticRole) {
		httpapi.WriteError(w, http.StatusConflict, "automatic_role", err.Error())
		return
	}
	if errors.Is(err, ErrLastAdmin) {
		httpapi.WriteError(w, http.StatusConflict, "last_admin", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not remove role")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado el rol «%s» a %s.",
		h.actorLogin(r.Context()), role.Name, r.PathValue("login")))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

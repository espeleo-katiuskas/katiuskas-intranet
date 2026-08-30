package roles

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
)

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
}

func NewHandler(service *Service, authenticator *auth.Authenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/roles", h.authenticator.Protect(h.list, "admin", "board"))
	mux.Handle("POST /api/v1/roles", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("DELETE /api/v1/roles/{id}", h.authenticator.Protect(h.delete, "admin"))

	mux.Handle("POST /api/v1/users/{userID}/roles", h.authenticator.Protect(h.assign, "admin", "board"))
	mux.Handle("DELETE /api/v1/users/{userID}/roles/{roleID}", h.authenticator.Protect(h.remove, "admin", "board"))
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
	httpapi.WriteJSON(w, http.StatusCreated, role)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
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
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
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
	current, _ := auth.UserFromContext(r.Context())
	if role.Name == "admin" && !current.HasAnyRole("admin") {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only an admin can grant the admin role")
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
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
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
	current, _ := auth.UserFromContext(r.Context())
	if role.Name == "admin" && !current.HasAnyRole("admin") {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only an admin can revoke the admin role")
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
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

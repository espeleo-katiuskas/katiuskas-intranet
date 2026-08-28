package users

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/users", func(r chi.Router) {
		r.With(auth.RequireRole("admin", "board")).Get("/", h.list)
		r.With(auth.RequireRole("admin", "board")).Post("/", h.create)
		r.Get("/{id}", h.get)
		r.Patch("/{id}", h.update)
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, err := h.service.List(r.Context(), limit, offset)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list users")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if in.Role == "" {
		in.Role = RoleMember
	}
	user, err := h.service.Create(r.Context(), in)
	if err != nil {
		httpapi.WriteError(w, http.StatusConflict, "create_failed", err.Error())
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, user)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.Role == string(RoleMember) && current.ID != id {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own profile")
		return
	}
	user, err := h.service.Get(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.Role == string(RoleMember) && current.ID != id {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only edit their own profile")
		return
	}
	var in UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	user, err := h.service.Update(r.Context(), id, in)
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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
	mux.Handle("GET /api/v1/users", h.authenticator.Protect(h.list, "admin", "board"))
	mux.Handle("POST /api/v1/users", h.authenticator.Protect(h.create, "admin", "board"))
	mux.Handle("GET /api/v1/users/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/users/{id}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/users/{id}", h.authenticator.Protect(h.delete, "admin", "board"))
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
	var body struct {
		FirstName             string  `json:"first_name"`
		LastName              string  `json:"last_name"`
		NationalID            *string `json:"national_id"`
		BirthDate             *string `json:"birth_date"`
		Email                 string  `json:"email"`
		Phone                 *string `json:"phone"`
		Address               *string `json:"address"`
		EmergencyContactName  *string `json:"emergency_contact_name"`
		EmergencyContactPhone *string `json:"emergency_contact_phone"`
		Username              string  `json:"username"`
		Password              string  `json:"password"`
		Role                  Role    `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	var birthDate *time.Time
	if body.BirthDate != nil && *body.BirthDate != "" {
		d, err := time.Parse("2006-01-02", *body.BirthDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "birth_date must be YYYY-MM-DD")
			return
		}
		birthDate = &d
	}

	role := body.Role
	if role == "" {
		role = RoleMember
	}

	user, err := h.service.Create(r.Context(), CreateInput{
		FirstName:             body.FirstName,
		LastName:              body.LastName,
		NationalID:            body.NationalID,
		BirthDate:             birthDate,
		Email:                 body.Email,
		Phone:                 body.Phone,
		Address:               body.Address,
		EmergencyContactName:  body.EmergencyContactName,
		EmergencyContactPhone: body.EmergencyContactPhone,
		Username:              body.Username,
		Password:              body.Password,
		Role:                  role,
	})
	if err != nil {
		httpapi.WriteError(w, http.StatusConflict, "create_failed", err.Error())
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, user)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.ID == id {
		httpapi.WriteError(w, http.StatusConflict, "cannot_delete_self", "you cannot delete your own account")
		return
	}
	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, ErrHasReferences) {
		httpapi.WriteError(w, http.StatusConflict, "has_references", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete user")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

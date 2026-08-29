package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/roles"
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
	mux.Handle("GET /api/v1/users/directory", h.authenticator.Protect(h.directory))
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

// directory lists everyone with access, name only, so any signed-in
// member can pick a person for an activity's leader or participant list
// without seeing the personal data GET /users exposes.
func (h *Handler) directory(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.Directory(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list directory")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FirstName        string   `json:"first_name"`
		LastName         string   `json:"last_name"`
		NationalID       *string  `json:"national_id"`
		BirthDate        *string  `json:"birth_date"`
		Email            string   `json:"email"`
		Phone            *string  `json:"phone"`
		Address          *string  `json:"address"`
		PostalCode       *string  `json:"postal_code"`
		City             *string  `json:"city"`
		Province         *string  `json:"province"`
		EmergencyContact *string  `json:"emergency_contact"`
		GrantAccess      bool     `json:"grant_access"`
		RoleNames        []string `json:"role_names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	birthDate, err := parseOptionalDate(body.BirthDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "birth_date must be YYYY-MM-DD")
		return
	}
	if body.GrantAccess && (body.Phone == nil || *body.Phone == "") {
		httpapi.WriteError(w, http.StatusBadRequest, "phone_required",
			"phone is required to grant access: sign-in links are only sent when email and phone both match")
		return
	}

	user, err := h.service.Create(r.Context(), CreateInput{
		FirstName:        body.FirstName,
		LastName:         body.LastName,
		NationalID:       body.NationalID,
		BirthDate:        birthDate,
		Email:            body.Email,
		Phone:            body.Phone,
		Address:          body.Address,
		PostalCode:       body.PostalCode,
		City:             body.City,
		Province:         body.Province,
		EmergencyContact: body.EmergencyContact,
		GrantAccess:      body.GrantAccess,
		RoleNames:        body.RoleNames,
	})
	if errors.Is(err, ErrDuplicate) {
		httpapi.WriteError(w, http.StatusConflict, "duplicate", err.Error())
		return
	}
	if errors.Is(err, roles.ErrNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_role", "one of the given role names doesn't exist")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create user")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, user)
}

func parseOptionalDate(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", *raw)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != id {
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
	if !current.HasAnyRole("admin", "board") && current.ID != id {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only edit their own profile")
		return
	}
	var body struct {
		FirstName        *string `json:"first_name"`
		LastName         *string `json:"last_name"`
		Email            *string `json:"email"`
		NationalID       *string `json:"national_id"`
		BirthDate        *string `json:"birth_date"`
		Phone            *string `json:"phone"`
		Address          *string `json:"address"`
		PostalCode       *string `json:"postal_code"`
		City             *string `json:"city"`
		Province         *string `json:"province"`
		EmergencyContact *string `json:"emergency_contact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	birthDate, err := parseOptionalDate(body.BirthDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "birth_date must be YYYY-MM-DD")
		return
	}

	user, err := h.service.Update(r.Context(), id, UpdateInput{
		FirstName:        body.FirstName,
		LastName:         body.LastName,
		Email:            body.Email,
		NationalID:       body.NationalID,
		BirthDate:        birthDate,
		Phone:            body.Phone,
		Address:          body.Address,
		PostalCode:       body.PostalCode,
		City:             body.City,
		Province:         body.Province,
		EmergencyContact: body.EmergencyContact,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, ErrDuplicate) {
		httpapi.WriteError(w, http.StatusConflict, "duplicate", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update user")
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
	if errors.Is(err, roles.ErrLastAdmin) {
		httpapi.WriteError(w, http.StatusConflict, "last_admin", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete user")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

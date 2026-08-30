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
	mux.Handle("GET /api/v1/users", h.authenticator.Protect(h.list, "admin", "board", "member"))
	mux.Handle("POST /api/v1/users", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("GET /api/v1/users/directory", h.authenticator.Protect(h.directory))
	mux.Handle("GET /api/v1/users/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/users/{id}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/users/{id}", h.authenticator.Protect(h.delete, "admin"))
}

// list is the only place a plain member can see anyone's data besides
// their own: admin/board get the full picture, everyone else gets
// MemberListing's narrower view of every person (not just themselves) —
// see its doc comment for exactly what that includes.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, err := h.service.List(r.Context(), limit, offset)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list users")
		return
	}

	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") {
		restricted := make([]MemberListing, len(list))
		for i, u := range list {
			restricted[i] = u.ForMemberListing()
		}
		httpapi.WriteJSON(w, http.StatusOK, restricted)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// directory lists everyone on file, name only, so any signed-in member
// can pick a person for an activity's leader or participant list
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
		Gender           *Gender  `json:"gender"`
		Email            string   `json:"email"`
		Phone            *string  `json:"phone"`
		Address          *string  `json:"address"`
		PostalCode       *string  `json:"postal_code"`
		City             *string  `json:"city"`
		Province         *string  `json:"province"`
		EmergencyContact *string  `json:"emergency_contact"`
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

	user, err := h.service.Create(r.Context(), CreateInput{
		FirstName:        body.FirstName,
		LastName:         body.LastName,
		NationalID:       body.NationalID,
		BirthDate:        birthDate,
		Gender:           body.Gender,
		Email:            body.Email,
		Phone:            body.Phone,
		Address:          body.Address,
		PostalCode:       body.PostalCode,
		City:             body.City,
		Province:         body.Province,
		EmergencyContact: body.EmergencyContact,
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
	if errors.Is(err, roles.ErrAutomaticRole) {
		httpapi.WriteError(w, http.StatusBadRequest, "automatic_role", err.Error())
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

// update lets admin edit anyone's full record, and anyone else edit only
// their own — and even then, only the contact fields (see UpdateInput's
// doc comment): board lost the general ability to modify a person's
// data along with everyone else who isn't admin, and self-service edits
// were never meant to touch identity fields like name, DNI, birth date,
// or gender.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	isAdmin := current.HasAnyRole("admin")
	if !isAdmin && current.ID != id {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "you can only edit your own profile")
		return
	}
	var body struct {
		FirstName        *string `json:"first_name"`
		LastName         *string `json:"last_name"`
		Email            *string `json:"email"`
		NationalID       *string `json:"national_id"`
		BirthDate        *string `json:"birth_date"`
		Gender           *Gender `json:"gender"`
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
	if !isAdmin && (body.FirstName != nil || body.LastName != nil || body.NationalID != nil || body.BirthDate != nil || body.Gender != nil) {
		httpapi.WriteError(w, http.StatusForbidden, "restricted_fields",
			"you can only edit email, phone, address, postal_code, city, province, and emergency_contact")
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
		Gender:           body.Gender,
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

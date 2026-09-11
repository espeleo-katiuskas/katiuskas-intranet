package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
)

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — every write endpoint in
// this handler needs it, so it's pulled out rather than repeated at
// each call site. Falls back to a placeholder rather than failing the
// whole request if the lookup itself errors, since a notification is
// never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.service.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users", h.authenticator.Protect(h.list, "admin", "board", "member"))
	mux.Handle("POST /api/v1/users", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("GET /api/v1/users/directory", h.authenticator.Protect(h.directory))
	mux.Handle("GET /api/v1/users/{login}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/users/{login}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/users/{login}", h.authenticator.Protect(h.delete, "admin"))
}

// list is the only place a plain member can see anyone's data besides
// their own: admin/board get the full picture, everyone else gets
// MemberListing's narrower view — and only of people who are or have
// been a socio (a non-nil MembershipStartDate), not everyone on file —
// see MemberListing's own doc comment for exactly what that includes.
// Unpaginated, deliberately: membership grows by roughly 15 people a
// year, nowhere near the scale where returning everyone on file in one
// response would be a real cost.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	current, _ := auth.UserFromContext(r.Context())
	onlySocios := !current.HasAnyRole("admin", "board")
	list, err := h.service.List(r.Context(), onlySocios)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list users")
		return
	}

	if onlySocios {
		restricted := make([]MemberListing, 0, len(list))
		for _, u := range list {
			restricted = append(restricted, u.ForMemberListing())
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
		BirthDate        string   `json:"birth_date"`
		Gender           *Gender  `json:"gender"`
		Email            *string  `json:"email"`
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

	birthDate, err := time.Parse("2006-01-02", body.BirthDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "birth_date is required and must be YYYY-MM-DD")
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
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado la persona %s (%s %s).",
		h.actorLogin(r.Context()), user.Login, user.FirstName, user.LastName))
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

// resolveLogin looks up the numeric id behind a {login} path segment,
// writing a 404 (never revealing whether the login was merely malformed)
// and reporting ok=false if it doesn't belong to anyone.
func (h *Handler) resolveLogin(w http.ResponseWriter, r *http.Request) (id int64, ok bool) {
	id, err := h.service.UserIDForLogin(r.Context(), r.PathValue("login"))
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not look up user")
		return 0, false
	}
	return id, true
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveLogin(w, r)
	if !ok {
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
	id, ok := h.resolveLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	isAdmin := current.HasAnyRole("admin")
	if !isAdmin && current.ID != id {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "you can only edit your own profile")
		return
	}
	var body struct {
		Login            *string          `json:"login"`
		FirstName        *string          `json:"first_name"`
		LastName         *string          `json:"last_name"`
		BirthDate        *string          `json:"birth_date"`
		Email            Nullable[string] `json:"email"`
		NationalID       Nullable[string] `json:"national_id"`
		Gender           Nullable[Gender] `json:"gender"`
		Phone            Nullable[string] `json:"phone"`
		Address          Nullable[string] `json:"address"`
		PostalCode       Nullable[string] `json:"postal_code"`
		City             Nullable[string] `json:"city"`
		Province         Nullable[string] `json:"province"`
		EmergencyContact Nullable[string] `json:"emergency_contact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if !isAdmin && (body.Login != nil || body.FirstName != nil || body.LastName != nil || body.NationalID.Set || body.BirthDate != nil || body.Gender.Set) {
		httpapi.WriteError(w, http.StatusForbidden, "restricted_fields",
			"you can only edit email, phone, address, postal_code, city, province, and emergency_contact")
		return
	}

	birthDate, err := parseOptionalDate(body.BirthDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "birth_date must be YYYY-MM-DD")
		return
	}

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below (see auditableFields)
	// — a failure here isn't reason enough to fail the actual edit, so
	// it's only ever consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), id)

	user, err := h.service.Update(r.Context(), id, UpdateInput{
		Login:            body.Login,
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
	if errors.Is(err, ErrInvalidLogin) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_login", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update user")
		return
	}
	if beforeErr == nil {
		if diff := httpapi.DiffFields(before, user, auditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado a %s:\n%s",
				h.actorLogin(r.Context()), before.Login, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

// auditableFields are the User fields update's own mutation
// notification diffs (see httpapi.DiffFields) — matching UpdateInput's
// field set exactly, so a computed/derived field this endpoint can't
// touch at all (Roles, a membership or license date, last login...)
// never shows up as a spurious "change" just because it happened to be
// recomputed between the before and after fetch.
var auditableFields = []string{
	"Login", "FirstName", "LastName", "BirthDate", "Email", "NationalID",
	"Gender", "Phone", "Address", "PostalCode", "City", "Province", "EmergencyContact",
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.ID == id {
		httpapi.WriteError(w, http.StatusConflict, "cannot_delete_self", "you cannot delete your own account")
		return
	}
	// Fetched before deleting, purely so the mutation notification below
	// can name who was deleted — there's nothing left to look up once
	// Delete succeeds.
	target, targetErr := h.service.Get(r.Context(), id)

	err := h.service.Delete(r.Context(), id)
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
	if targetErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado a %s (%s %s).",
			h.actorLogin(r.Context()), target.Login, target.FirstName, target.LastName))
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

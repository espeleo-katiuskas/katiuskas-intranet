package licenses

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

// POST/PATCH/DELETE are admin-only: board can see everyone's licenses
// (read-only, like the rest of a person's data) but not assign, correct,
// or remove one.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/users/{userID}/licenses", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{userID}/licenses", h.authenticator.Protect(h.create, "admin"))
	mux.Handle("PATCH /api/v1/licenses/{id}", h.authenticator.Protect(h.update, "admin"))
	mux.Handle("DELETE /api/v1/licenses/{id}", h.authenticator.Protect(h.delete, "admin"))
	mux.Handle("GET /api/v1/license-types", h.authenticator.Protect(h.listTypes, "admin"))
}

// listTypes is admin-only, matching create: the catalog only exists to
// populate the "assign a license" form.
func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListTypes(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list license types")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own license history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list licenses")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	var body struct {
		LicenseTypeID int64   `json:"license_type_id"`
		OtherDetail   *string `json:"other_detail"`
		IssuedDate    string  `json:"issued_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.LicenseTypeID == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "license_type_id is required")
		return
	}
	issuedDate, err := time.Parse("2006-01-02", body.IssuedDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "issued_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.Create(r.Context(), CreateInput{
		UserID: userID, LicenseTypeID: body.LicenseTypeID, OtherDetail: body.OtherDetail, IssuedDate: issuedDate,
	})
	if errors.Is(err, ErrNotActiveMember) {
		httpapi.WriteError(w, http.StatusConflict, "not_active_member", err.Error())
		return
	}
	if errors.Is(err, ErrTypeNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_license_type", err.Error())
		return
	}
	if errors.Is(err, ErrOtherDetailRequired) || errors.Is(err, ErrOtherDetailNotAllowed) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_other_detail", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create license")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		LicenseTypeID int64   `json:"license_type_id"`
		OtherDetail   *string `json:"other_detail"`
		IssuedDate    string  `json:"issued_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if body.LicenseTypeID == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "license_type_id is required")
		return
	}
	issuedDate, err := time.Parse("2006-01-02", body.IssuedDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "issued_date must be YYYY-MM-DD")
		return
	}

	l, err := h.service.Update(r.Context(), id, UpdateInput{
		LicenseTypeID: body.LicenseTypeID, OtherDetail: body.OtherDetail, IssuedDate: issuedDate,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "license not found")
		return
	}
	if errors.Is(err, ErrTypeNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_license_type", err.Error())
		return
	}
	if errors.Is(err, ErrOtherDetailRequired) || errors.Is(err, ErrOtherDetailNotAllowed) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_other_detail", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update license")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	err = h.service.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "license not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete license")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

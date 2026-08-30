package memberships

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
	mux.Handle("GET /api/v1/memberships", h.authenticator.Protect(h.listActive, "admin", "board"))
	mux.Handle("PATCH /api/v1/memberships/{id}", h.authenticator.Protect(h.update, "admin", "board"))
	mux.Handle("GET /api/v1/users/{userID}/memberships", h.authenticator.Protect(h.listByUser))
	mux.Handle("POST /api/v1/users/{userID}/memberships", h.authenticator.Protect(h.create, "admin", "board"))
}

func (h *Handler) listActive(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListActive(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list memberships")
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
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own membership history")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list memberships")
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
		MembershipNumber *string `json:"membership_number"`
		StartDate        string  `json:"start_date"`
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
		UserID: userID, MembershipNumber: body.MembershipNumber, StartDate: startDate,
	})
	if errors.Is(err, ErrOverlap) {
		httpapi.WriteError(w, http.StatusConflict, "overlapping_period", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create membership")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, m)
}

// update covers both ending a membership (send just end_date) and fully
// correcting a period's dates or number later — start_date is always
// required and end_date always replaces whatever was there (null/omitted
// means the period is open), so there's no ambiguity about which fields
// a partial body was meant to leave alone versus clear.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		MembershipNumber *string `json:"membership_number"`
		StartDate        string  `json:"start_date"`
		EndDate          *string `json:"end_date"`
		EndReason        *string `json:"end_reason"`
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
		MembershipNumber: body.MembershipNumber, StartDate: startDate, EndDate: endDate, EndReason: body.EndReason,
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
	httpapi.WriteJSON(w, http.StatusOK, m)
}

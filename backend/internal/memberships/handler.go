package memberships

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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
	r.With(auth.RequireRole("admin", "board")).Get("/memberships", h.listActive)
	r.With(auth.RequireRole("admin", "board")).Patch("/memberships/{id}", h.end)

	r.Route("/users/{userID}/memberships", func(r chi.Router) {
		r.Get("/", h.listByUser)
		r.With(auth.RequireRole("admin", "board")).Post("/", h.create)
	})
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
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if current.Role == "member" && current.ID != userID {
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
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
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

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		EndDate   string  `json:"end_date"`
		EndReason *string `json:"end_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	endDate, err := time.Parse("2006-01-02", body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}
	m, err := h.service.End(r.Context(), id, EndInput{EndDate: endDate, EndReason: body.EndReason})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not end membership")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, m)
}

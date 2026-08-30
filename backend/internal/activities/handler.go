package activities

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
	mux.Handle("GET /api/v1/activities", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/activities", h.authenticator.Protect(h.create))
	mux.Handle("PATCH /api/v1/activities/{id}", h.authenticator.Protect(h.update, "admin", "board"))
	mux.Handle("DELETE /api/v1/activities/{id}", h.authenticator.Protect(h.cancel, "admin", "board"))

	mux.Handle("GET /api/v1/activities/{id}/participants", h.authenticator.Protect(h.listParticipants))
	mux.Handle("POST /api/v1/activities/{id}/participants", h.authenticator.Protect(h.join))
	mux.Handle("DELETE /api/v1/activities/{id}/participants/{userID}", h.authenticator.Protect(h.leave))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, err := h.service.List(r.Context(), limit, offset)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list activities")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title              string  `json:"title"`
		Type               Type    `json:"type"`
		StartDate          string  `json:"start_date"`
		EndDate            *string `json:"end_date"`
		Location           string  `json:"location"`
		Description        *string `json:"description"`
		Difficulty         *int16  `json:"difficulty"`
		MaxParticipants    *int32  `json:"max_participants"`
		LeaderID           *int64  `json:"leader_id"`
		ParticipantUserIDs []int64 `json:"participant_user_ids"`
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
	if body.EndDate != nil {
		d, err := time.Parse("2006-01-02", *body.EndDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
			return
		}
		endDate = &d
	}
	if body.Type == "" {
		body.Type = TypeCaving
	}
	a, err := h.service.Create(r.Context(), CreateInput{
		Title: body.Title, Type: body.Type, StartDate: startDate, EndDate: endDate,
		Location: body.Location, Description: body.Description, Difficulty: body.Difficulty,
		MaxParticipants: body.MaxParticipants, LeaderID: body.LeaderID,
		ParticipantUserIDs: body.ParticipantUserIDs,
	})
	if errors.Is(err, ErrParticipantNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_participant", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var in UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	a, err := h.service.Update(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	err = h.service.Cancel(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not cancel activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) listParticipants(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListParticipants(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list participants")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// join signs a person up as a participant. With no body (or an empty
// user_id) it signs up the caller ("Apuntarme"); admin/board can instead
// name someone else in the body to add them on that person's behalf. A
// plain member naming anyone other than themselves is forbidden.
func (h *Handler) join(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())

	var body struct {
		UserID *int64 `json:"user_id"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
			return
		}
	}

	userID := current.ID
	if body.UserID != nil {
		userID = *body.UserID
	}
	if !current.HasAnyRole("admin", "board") && userID != current.ID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only add themselves")
		return
	}

	if err := h.service.Join(r.Context(), id, userID); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not join activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) leave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only remove themselves")
		return
	}
	if err := h.service.Leave(r.Context(), id, userID); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not leave activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

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
	mux.Handle("GET /api/v1/activities/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/activities/{id}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/activities/{id}", h.authenticator.Protect(h.cancel))

	mux.Handle("GET /api/v1/activities/{id}/participants", h.authenticator.Protect(h.listParticipants))
	mux.Handle("POST /api/v1/activities/{id}/participants", h.authenticator.Protect(h.join))
	mux.Handle("DELETE /api/v1/activities/{id}/participants/{userID}", h.authenticator.Protect(h.leave))

	mux.Handle("GET /api/v1/users/{userID}/activities", h.authenticator.Protect(h.listByUser))
}

// list defaults to every activity (paginated), matching how it always
// worked; passing ?status=active switches to only the ones still needing
// attention (see Repository.ListActive) — with years of history piling
// up, that's what most callers actually want day to day.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	var (
		list []Activity
		err  error
	)
	if r.URL.Query().Get("status") == "active" {
		list, err = h.service.ListActive(r.Context(), limit, offset)
	} else {
		list, err = h.service.List(r.Context(), limit, offset)
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list activities")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title              string     `json:"title"`
		Type               Type       `json:"type"`
		StartDate          string     `json:"start_date"`
		EndDate            *string    `json:"end_date"`
		Location           string     `json:"location"`
		Description        *string    `json:"description"`
		Difficulty         *int16     `json:"difficulty"`
		MaxParticipants    *int32     `json:"max_participants"`
		Visibility         Visibility `json:"visibility"`
		ParticipantUserIDs []int64    `json:"participant_user_ids"`
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
	if body.Visibility == "" {
		body.Visibility = VisibilityPublic
	}
	current, _ := auth.UserFromContext(r.Context())
	a, err := h.service.Create(r.Context(), CreateInput{
		Title: body.Title, Type: body.Type, StartDate: startDate, EndDate: endDate,
		Location: body.Location, Description: body.Description, Difficulty: body.Difficulty,
		MaxParticipants: body.MaxParticipants, LeaderID: current.ID, Visibility: body.Visibility,
		ParticipantUserIDs: body.ParticipantUserIDs,
	})
	if errors.Is(err, ErrParticipantNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_participant", err.Error())
		return
	}
	if errors.Is(err, ErrNotFederated) {
		httpapi.WriteError(w, http.StatusConflict, "not_federated", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	a, err := h.service.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not fetch activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

// canManage reports whether the current user may edit or cancel the
// given activity: admin/board can manage any activity, and the leader
// who created it can manage their own.
func (h *Handler) canManage(r *http.Request, activityID int64) bool {
	current, _ := auth.UserFromContext(r.Context())
	if current.HasAnyRole("admin", "board") {
		return true
	}
	a, err := h.service.Get(r.Context(), activityID)
	if err != nil {
		return false
	}
	return a.LeaderID == current.ID
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	if !h.canManage(r, id) {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, board, or admin can edit this activity")
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
	if !h.canManage(r, id) {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, board, or admin can cancel this activity")
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

// listByUser is open to any signed-in baseline user, not just admin/board
// or the person themselves — who took part in an activity is already
// visible to anyone via listParticipants, so this is just the same
// information viewed from the other direction.
func (h *Handler) listByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "user id must be numeric")
		return
	}
	list, err := h.service.ListByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list activities")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
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
// user_id) it signs up the caller ("Apuntarme"); admin/board (or, on a
// private activity, the organizer too) can instead name someone else in
// the body to add them on that person's behalf.
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

	activity, err := h.service.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not fetch activity")
		return
	}

	// A private activity only gains participants through the organizer,
	// board, or admin — even signing *yourself* up needs one of them to
	// do it. A public activity works exactly as it always has: anyone
	// can sign themselves up, and admin/board can add anyone else — the
	// organizer gets no extra say over who joins their own public
	// activity.
	isStaff := current.HasAnyRole("admin", "board")
	if activity.Visibility == VisibilityPrivate {
		if !isStaff && current.ID != activity.LeaderID {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, board, or admin can add participants to a private activity")
			return
		}
	} else if !isStaff && userID != current.ID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only add themselves")
		return
	}

	err = h.service.Join(r.Context(), id, userID)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if errors.Is(err, ErrNotFederated) {
		httpapi.WriteError(w, http.StatusConflict, "not_federated", err.Error())
		return
	}
	if err != nil {
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

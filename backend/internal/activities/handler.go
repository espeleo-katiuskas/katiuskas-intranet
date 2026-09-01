package activities

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/activities
// (or name a person by login in a request body), but activities and
// activity_participants are keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without
// activities having to import that package (it can't: users already
// imports this one, via licenses).
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/activities", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/activities", h.authenticator.Protect(h.create))
	mux.Handle("GET /api/v1/activities/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/activities/{id}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/activities/{id}", h.authenticator.Protect(h.cancel))

	mux.Handle("GET /api/v1/activities/{id}/participants", h.authenticator.Protect(h.listParticipants))
	mux.Handle("POST /api/v1/activities/{id}/participants", h.authenticator.Protect(h.join))
	mux.Handle("DELETE /api/v1/activities/{id}/participants/{login}", h.authenticator.Protect(h.leave))

	mux.Handle("GET /api/v1/users/{login}/activities", h.authenticator.Protect(h.listByUser))
}

// resolveUserLogin looks up the numeric id behind a {login} path
// segment. Any failure — malformed, or simply nobody by that login — is
// reported the same way (404): this package only needs to know whether
// the lookup succeeded, not why it didn't.
func (h *Handler) resolveUserLogin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	return id, true
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
		Title             string     `json:"title"`
		Type              Type       `json:"type"`
		StartDate         string     `json:"start_date"`
		EndDate           string     `json:"end_date"`
		Description       *string    `json:"description"`
		Visibility        Visibility `json:"visibility"`
		ParticipantLogins []string   `json:"participant_logins"`
		LeaderLogin       *string    `json:"leader_login"`
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
	endDate, err := time.Parse("2006-01-02", body.EndDate)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}
	if body.Type == "" {
		body.Type = TypeCaving
	}
	if body.Visibility == "" {
		body.Visibility = VisibilityPublic
	}
	participantIDs := make([]int64, 0, len(body.ParticipantLogins))
	for _, login := range body.ParticipantLogins {
		id, err := h.users.UserIDForLogin(r.Context(), login)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "unknown_participant", "one of the given participants does not exist")
			return
		}
		participantIDs = append(participantIDs, id)
	}
	current, _ := auth.UserFromContext(r.Context())
	leaderID := current.ID
	// A member creating an activity always leads it themselves; only
	// admin/board can name someone else as the organizer instead.
	if body.LeaderLogin != nil {
		if !current.HasAnyRole("admin", "board") {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only admin or board can choose a different organizer")
			return
		}
		id, err := h.users.UserIDForLogin(r.Context(), *body.LeaderLogin)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "leader not found")
			return
		}
		leaderID = id
	}
	a, err := h.service.Create(r.Context(), CreateInput{
		Title: body.Title, Type: body.Type, StartDate: startDate, EndDate: endDate,
		Description: body.Description, LeaderID: leaderID, Visibility: body.Visibility,
		ParticipantUserIDs: participantIDs,
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
	if leaderLogin, err := h.users.LoginForUserID(r.Context(), leaderID); err == nil {
		a.LeaderLogin = leaderLogin
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
	var body struct {
		Title       *string     `json:"title"`
		Type        *Type       `json:"type"`
		StartDate   *string     `json:"start_date"`
		EndDate     *string     `json:"end_date"`
		Description *string     `json:"description"`
		Status      *Status     `json:"status"`
		Visibility  *Visibility `json:"visibility"`
		LeaderLogin *string     `json:"leader_login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	in := UpdateInput{
		Title: body.Title, Type: body.Type, Description: body.Description,
		Status: body.Status, Visibility: body.Visibility,
	}
	// Reassigning the organizer is admin/board only — unlike every other
	// field here, the current leader can't do this for their own
	// activity (they could otherwise hand it off to dodge canManage).
	if body.LeaderLogin != nil {
		current, _ := auth.UserFromContext(r.Context())
		if !current.HasAnyRole("admin", "board") {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only admin or board can change an activity's organizer")
			return
		}
		leaderID, err := h.users.UserIDForLogin(r.Context(), *body.LeaderLogin)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "leader not found")
			return
		}
		in.LeaderID = &leaderID
	}
	if body.StartDate != nil {
		d, err := time.Parse("2006-01-02", *body.StartDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
			return
		}
		in.StartDate = &d
	}
	if body.EndDate != nil {
		d, err := time.Parse("2006-01-02", *body.EndDate)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
			return
		}
		in.EndDate = &d
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
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
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
		Login *string `json:"login"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
			return
		}
	}

	userID := current.ID
	if body.Login != nil {
		id, err := h.users.UserIDForLogin(r.Context(), *body.Login)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		userID = id
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
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
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

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/activities
// (or name a person by login in a request body), but activities and
// activity_participants are keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without
// activities having to import that package directly. licenses/
// memberships/roles genuinely can't (users.Service imports all three);
// activities keeps the same small local-interface shape anyway for
// consistency across every package that's keyed by user id.
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — every write endpoint in
// this handler needs it, so it's pulled out rather than repeated at
// each call site. Falls back to a placeholder rather than failing the
// whole request if the lookup itself errors, since a notification is
// never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// auditableFields are the Activity fields update's own mutation
// notification diffs (see httpapi.DiffFields) — matching UpdateInput's
// field set exactly (LeaderLogin standing in for the underlying
// LeaderID, which is a raw numeric id and not human-readable), so
// nothing this endpoint can't actually touch shows up as a spurious
// "change".
var auditableFields = []string{
	"Title", "Type", "StartDate", "EndDate", "Description", "Visibility", "MaxParticipants", "LeaderLogin",
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/activities", h.authenticator.Protect(h.list))
	mux.Handle("POST /api/v1/activities", h.authenticator.Protect(h.create))
	mux.Handle("GET /api/v1/activities/{id}", h.authenticator.Protect(h.get))
	mux.Handle("PATCH /api/v1/activities/{id}", h.authenticator.Protect(h.update))
	mux.Handle("DELETE /api/v1/activities/{id}", h.authenticator.Protect(h.deleteActivity))

	mux.Handle("GET /api/v1/activities/{id}/guests", h.authenticator.Protect(h.listGuests))
	mux.Handle("POST /api/v1/activities/{id}/guests", h.authenticator.Protect(h.addGuest))
	mux.Handle("DELETE /api/v1/activities/{id}/guests/{guestID}", h.authenticator.Protect(h.deleteGuest))

	mux.Handle("GET /api/v1/activities/{id}/participants", h.authenticator.Protect(h.listParticipants))
	mux.Handle("POST /api/v1/activities/{id}/participants", h.authenticator.Protect(h.join))
	mux.Handle("DELETE /api/v1/activities/{id}/participants/{login}", h.authenticator.Protect(h.leave))

	mux.Handle("GET /api/v1/users/{login}/activities", h.authenticator.Protect(h.listByUser))

	mux.Handle("GET /api/v1/holidays", h.authenticator.Protect(h.listHolidays))
	mux.Handle("POST /api/v1/holidays", h.authenticator.Protect(h.createHoliday, "admin", "board"))
	mux.Handle("DELETE /api/v1/holidays/{id}", h.authenticator.Protect(h.deleteHoliday, "admin", "board"))
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
// worked; passing ?active=true switches to only the ones still needing
// attention (see Repository.ListActive) — with years of history piling
// up, that's what most callers actually want day to day. ?order=asc
// sorts oldest-first instead of the newest-first default.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	ascending := r.URL.Query().Get("order") == "asc"

	var (
		list []Activity
		err  error
	)
	if r.URL.Query().Get("active") == "true" {
		list, err = h.service.ListActive(r.Context(), limit, offset, ascending)
	} else {
		list, err = h.service.List(r.Context(), limit, offset, ascending)
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
		MaxParticipants   *int       `json:"max_participants"`
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
		ParticipantUserIDs: participantIDs, CreatedBy: current.ID, MaxParticipants: body.MaxParticipants,
	})
	if errors.Is(err, ErrInvalidMaxParticipants) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_max_participants", err.Error())
		return
	}
	if errors.Is(err, ErrActivityFull) {
		httpapi.WriteError(w, http.StatusConflict, "activity_full", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidType) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_type", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidVisibility) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_visibility", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidDateRange) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date_range", err.Error())
		return
	}
	if errors.Is(err, ErrParticipantNotFound) {
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_participant", err.Error())
		return
	}
	if errors.Is(err, ErrNotMember) {
		httpapi.WriteError(w, http.StatusConflict, "not_a_member", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create activity")
		return
	}
	if leaderLogin, err := h.users.LoginForUserID(r.Context(), leaderID); err == nil {
		a.LeaderLogin = leaderLogin
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha creado la actividad «%s» (%s).",
		h.actorLogin(r.Context()), a.Title, a.StartDate.Format("2006-01-02")))
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
		Cancelled   *bool       `json:"cancelled"`
		Visibility  *Visibility `json:"visibility"`
		LeaderLogin *string     `json:"leader_login"`
		// Absent leaves the limit as it is, null takes it off, a number sets it.
		MaxParticipants json.RawMessage `json:"max_participants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	in := UpdateInput{
		Title: body.Title, Type: body.Type, Description: body.Description,
		Cancelled: body.Cancelled, Visibility: body.Visibility,
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
	if body.MaxParticipants != nil {
		in.SetMaxParticipants = true
		if string(body.MaxParticipants) != "null" {
			var n int
			if err := json.Unmarshal(body.MaxParticipants, &n); err != nil {
				httpapi.WriteError(w, http.StatusBadRequest, "invalid_max_participants", "max_participants must be a whole number, or null for no limit")
				return
			}
			in.MaxParticipants = &n
		}
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

	// Fetched before applying anything, purely to describe exactly what
	// changed in the mutation notification below (see auditableFields)
	// — a failure here isn't reason enough to fail the actual edit, so
	// it's only ever consulted if it succeeded.
	before, beforeErr := h.service.Get(r.Context(), id)

	a, err := h.service.Update(r.Context(), id, in)
	if errors.Is(err, ErrInvalidMaxParticipants) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_max_participants", err.Error())
		return
	}
	if errors.Is(err, ErrMaxBelowAttendance) {
		httpapi.WriteError(w, http.StatusConflict, "max_below_attendance", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidType) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_type", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidVisibility) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_visibility", err.Error())
		return
	}
	if errors.Is(err, ErrInvalidDateRange) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date_range", err.Error())
		return
	}
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update activity")
		return
	}
	if beforeErr == nil {
		// Cancelling and reactivating are said in their own words, not as one more changed field.
		if before.Cancelled != a.Cancelled {
			verb := "ha cancelado"
			if !a.Cancelled {
				verb = "ha reactivado"
			}
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s %s la actividad «%s».",
				h.actorLogin(r.Context()), verb, before.Title))
		}
		if diff := httpapi.DiffFields(before, a, auditableFields...); len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado la actividad «%s»:\n%s",
				h.actorLogin(r.Context()), before.Title, strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, a)
}

// deleteActivity really deletes an activity, but only one with nothing attached (participants,
// resources, material movements): to take it out of sight without deleting it, cancel it with PATCH.
func (h *Handler) deleteActivity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	if !h.canManage(r, id) {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, board, or admin can delete this activity")
		return
	}
	// Fetched before deleting, so the notification below can still name the activity.
	target, targetErr := h.service.Get(r.Context(), id)

	err = h.service.Delete(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	case errors.Is(err, ErrActivityInUse):
		httpapi.WriteError(w, http.StatusConflict, "activity_in_use", err.Error())
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete activity")
		return
	}
	if targetErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado la actividad «%s».",
			h.actorLogin(r.Context()), target.Title))
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

	// In a public activity anybody can sign up anybody (themselves included). A private one only
	// gains participants through the organizer, admin, board, or somebody who is already signed up:
	// that is also who can sign *themselves* up, as nobody else can see it is there to ask.
	if activity.Visibility == VisibilityPrivate && !current.HasAnyRole("admin", "board") && current.ID != activity.LeaderID {
		isParticipant, err := h.service.IsParticipant(r.Context(), id, current.ID)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not check participation")
			return
		}
		if !isParticipant {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, admin, board or somebody already signed up can add participants to a private activity")
			return
		}
	}

	err = h.service.Join(r.Context(), id, userID, current.ID)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if errors.Is(err, ErrNotMember) {
		httpapi.WriteError(w, http.StatusConflict, "not_a_member", err.Error())
		return
	}
	if errors.Is(err, ErrActivityFull) {
		httpapi.WriteError(w, http.StatusConflict, "activity_full", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not join activity")
		return
	}
	// Distinguishes "signed themselves up" from "somebody signed up someone else" — the second is
	// worth naming both people, the first would just be saying the same login twice.
	if userID == current.ID {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s se ha apuntado a la actividad «%s».",
			h.actorLogin(r.Context()), activity.Title))
	} else {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha apuntado a %s a la actividad «%s».",
			h.actorLogin(r.Context()), *body.Login, activity.Title))
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

	// Fetched before the permission check below (not just for the
	// notification, as before) — a private activity's own organizer can
	// now remove a participant too, the mirror image of join's own
	// "organizer, board, or admin" gate for adding one to a private
	// activity, so answering that needs Activity.Visibility/LeaderID in
	// hand first. A nonexistent activity 404s here rather than falling
	// through to a self-or-admin-only check that can't tell whether the
	// caller organizes it.
	activity, err := h.service.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "activity not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not fetch activity")
		return
	}

	// Who may remove a participant: themselves, admin or board, a private activity's organizer, and
	// whoever signed them up (whatever the visibility) — somebody can always undo their own mistake.
	isOrganizerOfPrivate := activity.Visibility == VisibilityPrivate && current.ID == activity.LeaderID
	if !current.HasAnyRole("admin", "board") && !isOrganizerOfPrivate && current.ID != userID {
		addedBy, signedUp, err := h.service.AddedBy(r.Context(), id, userID)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not check who signed them up")
			return
		}
		if !signedUp || addedBy != current.ID {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the person themselves, whoever signed them up, the organizer of a private activity, admin or board can remove a participant")
			return
		}
	}

	if err := h.service.Leave(r.Context(), id, userID); err != nil {
		if errors.Is(err, ErrHasGuests) {
			httpapi.WriteError(w, http.StatusConflict, "has_guests", err.Error())
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not leave activity")
		return
	}
	targetLogin := r.PathValue("login")
	if current.ID == userID {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s se ha dado de baja de la actividad «%s».",
			targetLogin, activity.Title))
	} else {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha dado de baja a %s de la actividad «%s».",
			h.actorLogin(r.Context()), targetLogin, activity.Title))
	}
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// listHolidays backs the activities calendar view. year defaults to
// every year on file when omitted or not a number — there are few
// enough holidays overall that returning everything is a fine default,
// not something worth a 400 over.
func (h *Handler) listHolidays(w http.ResponseWriter, r *http.Request) {
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	list, err := h.service.ListHolidays(r.Context(), year)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list holidays")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createHoliday(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	date, err := time.Parse("2006-01-02", body.Date)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "date must be YYYY-MM-DD")
		return
	}
	if body.Name == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}
	holiday, err := h.service.CreateHoliday(r.Context(), date, body.Name)
	if errors.Is(err, ErrDuplicateHoliday) {
		httpapi.WriteError(w, http.StatusConflict, "duplicate_holiday", err.Error())
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create holiday")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha añadido el festivo «%s» (%s).",
		h.actorLogin(r.Context()), holiday.Name, holiday.Date.Format("2006-01-02")))
	httpapi.WriteJSON(w, http.StatusCreated, holiday)
}

func (h *Handler) deleteHoliday(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// DeleteHoliday itself returns the row it removed (DELETE ...
	// RETURNING), so the notification below can name it without a
	// separate fetch-before-delete.
	deleted, err := h.service.DeleteHoliday(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrHolidayNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "holiday not found")
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete holiday")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el festivo «%s» (%s).",
		h.actorLogin(r.Context()), deleted.Name, deleted.Date.Format("2006-01-02")))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// Guests are people from outside the club who go to an activity: a name, and the person of the club
// who invites them, who has to be signed up to it (table activity_guests).

var (
	// ErrInviterNotSignedUp means the person named as the one who invites is not signed up to the
	// activity: a guest comes with somebody who goes.
	ErrInviterNotSignedUp = errors.New("the person who invites has to be signed up to the activity")
	// ErrDuplicateGuest means that person already invited a guest with that name to this activity.
	ErrDuplicateGuest = errors.New("this person has already invited a guest with that name to the activity")
	// ErrGuestNotFound means the activity has no guest with that id.
	ErrGuestNotFound = errors.New("guest not found")
	// ErrHasGuests means a participant cannot be removed because they still have guests: those have to
	// be removed first (nothing cascades).
	ErrHasGuests = errors.New("this person still has guests in the activity: remove them first")
)

// Guest is one person from outside the club who goes to an activity, invited by a participant.
type Guest struct {
	ID         int64  `json:"id"`
	ActivityID int64  `json:"activity_id"`
	Name       string `json:"name"`
	InvitedBy  int64  `json:"-"`
	// InvitedByLogin is the person of the club who invites them (signed up to the activity);
	// AddedByLogin whoever recorded it (the same person, the organizer, admin or board) and AddedAt when.
	InvitedByLogin string    `json:"invited_by_login"`
	AddedByLogin   string    `json:"added_by_login"`
	AddedAt        time.Time `json:"added_at"`
}

const guestSelect = `
	g.id, g.activity_id, g.name, g.invited_by, ib.login, ab.login, g.added_at
	FROM activity_guests g
	JOIN users ib ON ib.id = g.invited_by
	JOIN users ab ON ab.id = g.added_by`

func scanGuest(row interface{ Scan(dest ...any) error }, g *Guest) error {
	return row.Scan(&g.ID, &g.ActivityID, &g.Name, &g.InvitedBy, &g.InvitedByLogin, &g.AddedByLogin, &g.AddedAt)
}

// ListGuests lists the guests of an activity, in the order they were added.
func (r *Repository) ListGuests(ctx context.Context, activityID int64) ([]Guest, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+guestSelect+` WHERE g.activity_id = $1 ORDER BY g.added_at, g.id`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Guest{}
	for rows.Next() {
		var g Guest
		if err := scanGuest(rows, &g); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// GetGuest looks up one guest of an activity.
func (r *Repository) GetGuest(ctx context.Context, activityID, id int64) (Guest, error) {
	var g Guest
	err := scanGuest(r.pool.QueryRow(ctx, `SELECT `+guestSelect+` WHERE g.id = $1 AND g.activity_id = $2`, id, activityID), &g)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrGuestNotFound
	}
	return g, err
}

// AddGuest records a guest invited by a person signed up to the activity.
func (r *Repository) AddGuest(ctx context.Context, activityID int64, name string, invitedBy, addedBy int64) (Guest, error) {
	// In its own (sub)transaction so a refused insert is rolled back on its own: under test, where
	// every call shares one outer transaction, the violation would otherwise abort all of it.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Guest{}, err
	}
	defer tx.Rollback(ctx)
	max, err := lockActivity(ctx, tx, activityID)
	if err != nil {
		return Guest{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO activity_guests (activity_id, name, invited_by, added_by) VALUES ($1, $2, $3, $4) RETURNING id
	`, activityID, name, invitedBy, addedBy).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case foreignKeyViolationCode:
			return Guest{}, ErrInviterNotSignedUp
		case uniqueViolationCode:
			return Guest{}, ErrDuplicateGuest
		}
	}
	if err != nil {
		return Guest{}, err
	}
	if err := checkRoom(ctx, tx, activityID, max); err != nil {
		return Guest{}, err
	}
	var g Guest
	if err := scanGuest(tx.QueryRow(ctx, `SELECT `+guestSelect+` WHERE g.id = $1`, id), &g); err != nil {
		return Guest{}, err
	}
	return g, tx.Commit(ctx)
}

// DeleteGuest removes a guest of an activity.
func (r *Repository) DeleteGuest(ctx context.Context, activityID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM activity_guests WHERE id = $1 AND activity_id = $2`, id, activityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGuestNotFound
	}
	return nil
}

func (s *Service) ListGuests(ctx context.Context, activityID int64) ([]Guest, error) {
	return s.repo.ListGuests(ctx, activityID)
}

func (s *Service) GetGuest(ctx context.Context, activityID, id int64) (Guest, error) {
	return s.repo.GetGuest(ctx, activityID, id)
}

func (s *Service) AddGuest(ctx context.Context, activityID int64, name string, invitedBy, addedBy int64) (Guest, error) {
	return s.repo.AddGuest(ctx, activityID, name, invitedBy, addedBy)
}

func (s *Service) DeleteGuest(ctx context.Context, activityID, id int64) error {
	return s.repo.DeleteGuest(ctx, activityID, id)
}

func (h *Handler) listGuests(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListGuests(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list guests")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// canInviteForOthers says whether the caller may record a guest on behalf of somebody else, and take
// one off: the organizer, admin and board. Anybody else only does it for the guests they invite.
func canInviteForOthers(current auth.AuthenticatedUser, activity Activity) bool {
	return current.HasAnyRole("admin", "board") || current.ID == activity.LeaderID
}

// addGuest records a guest. Whoever invites them (signed up to the activity) just gives the name; the
// organizer, admin and board also say which of the people signed up is the one who invites them.
func (h *Handler) addGuest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Name      string  `json:"name"`
		InvitedBy *string `json:"invited_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "name is required")
		return
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
	current, _ := auth.UserFromContext(r.Context())

	var inviterID int64
	if canInviteForOthers(current, activity) {
		if body.InvitedBy == nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invited_by_required", "invited_by must say which of the people signed up invites the guest")
			return
		}
		inviterID, err = h.users.UserIDForLogin(r.Context(), *body.InvitedBy)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
	} else {
		signedUp, err := h.service.IsParticipant(r.Context(), id, current.ID)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not check participation")
			return
		}
		if !signedUp {
			httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only somebody signed up to the activity, its organizer, admin or board can add guests")
			return
		}
		inviterID = current.ID
		if body.InvitedBy != nil {
			named, err := h.users.UserIDForLogin(r.Context(), *body.InvitedBy)
			if err != nil || named != current.ID {
				httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only the organizer, admin or board can add a guest on behalf of somebody else")
				return
			}
		}
	}

	guest, err := h.service.AddGuest(r.Context(), id, name, inviterID, current.ID)
	switch {
	case errors.Is(err, ErrInviterNotSignedUp):
		httpapi.WriteError(w, http.StatusConflict, "inviter_not_signed_up", err.Error())
		return
	case errors.Is(err, ErrDuplicateGuest):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_guest", err.Error())
		return
	case errors.Is(err, ErrActivityFull):
		httpapi.WriteError(w, http.StatusConflict, "activity_full", err.Error())
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not add guest")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("➕ %s ha apuntado a %s, invitado/a de %s, a la actividad «%s».",
		h.actorLogin(r.Context()), guest.Name, guest.InvitedByLogin, activity.Title))
	httpapi.WriteJSON(w, http.StatusCreated, guest)
}

// deleteGuest removes a guest: whoever invited them, the organizer, admin and board.
func (h *Handler) deleteGuest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	guestID, err := strconv.ParseInt(r.PathValue("guestID"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "guestID must be numeric")
		return
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
	guest, err := h.service.GetGuest(r.Context(), id, guestID)
	if errors.Is(err, ErrGuestNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "guest not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not fetch guest")
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !canInviteForOthers(current, activity) && current.ID != guest.InvitedBy {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "only whoever invited the guest, the organizer, admin or board can remove them")
		return
	}
	if err := h.service.DeleteGuest(r.Context(), id, guestID); err != nil {
		if errors.Is(err, ErrGuestNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "guest not found")
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not remove guest")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha quitado a %s, invitado/a de %s, de la actividad «%s».",
		h.actorLogin(r.Context()), guest.Name, guest.InvitedByLogin, activity.Title))
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

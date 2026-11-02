package activities

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("activity not found")
	// ErrActivityInUse means the activity cannot be deleted because something is attached to it:
	// participants, resources or material movements.
	ErrActivityInUse = errors.New("the activity still has participants, resources or material movements: remove them first")
	// ErrParticipantNotFound means one of the given participant user ids
	// doesn't exist. Create rejects the whole activity rather than
	// creating it with a partial participant list.
	ErrParticipantNotFound = errors.New("one of the given participants does not exist")
	// ErrHolidayNotFound means the given holiday id doesn't exist.
	ErrHolidayNotFound = errors.New("holiday not found")
	// ErrDuplicateHoliday means a holiday already exists on that date —
	// see the holidays.date UNIQUE constraint.
	ErrDuplicateHoliday = errors.New("a holiday already exists on that date")
	// ErrInvalidType and ErrInvalidVisibility mean the
	// given value isn't one of that enum's values (see Type.Valid).
	ErrInvalidType       = errors.New("type must be caving, canyoning, training, social or other")
	ErrInvalidVisibility = errors.New("visibility must be public or private")
	// ErrInvalidDateRange means end_date is earlier than start_date — the
	// end_not_before_start CHECK would reject it too, but as a 500.
	ErrInvalidDateRange = errors.New("end_date must not be earlier than start_date")
)

const foreignKeyViolationCode = "23503"
const uniqueViolationCode = "23505"

// activitySelect is appended to every query that returns one or more
// Activity rows other than Create's INSERT ... RETURNING (which can't
// JOIN against the row it just wrote) — every listing here can span
// activities led by different people, so the organizer's login is
// joined in rather than resolved one at a time.
const activitySelect = `
	a.id, a.title, a.type, a.start_date, a.end_date, a.description,
	a.cancelled, a.leader_id, ul.login, a.visibility
	FROM activities a
	JOIN users ul ON ul.id = a.leader_id`

// sortDirection returns a literal "ASC"/"DESC" to splice into an ORDER
// BY clause — safe to interpolate (unlike the bool it's derived from,
// nothing here ever comes from raw user input) since it's always one of
// these two fixed strings.
func sortDirection(ascending bool) string {
	if ascending {
		return "ASC"
	}
	return "DESC"
}

func scanActivity(row interface{ Scan(dest ...any) error }, a *Activity) error {
	return row.Scan(&a.ID, &a.Title, &a.Type, &a.StartDate, &a.EndDate, &a.Description,
		&a.Cancelled, &a.LeaderID, &a.LeaderLogin, &a.Visibility)
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// Create inserts the activity and signs up its initial participants (if
// any) in a single transaction, so an unknown participant id rolls back
// the whole thing instead of leaving a half-populated activity behind.
func (r *Repository) Create(ctx context.Context, in CreateInput) (Activity, error) {
	if in.EndDate.Before(in.StartDate) {
		return Activity{}, ErrInvalidDateRange
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Activity{}, err
	}
	defer tx.Rollback(ctx)

	var a Activity
	err = tx.QueryRow(ctx, `
		INSERT INTO activities (title, type, start_date, end_date, description, leader_id, visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, title, type, start_date, end_date, description, cancelled, leader_id, visibility
	`, in.Title, in.Type, in.StartDate, in.EndDate, in.Description, in.LeaderID, in.Visibility,
	).Scan(&a.ID, &a.Title, &a.Type, &a.StartDate, &a.EndDate, &a.Description, &a.Cancelled, &a.LeaderID, &a.Visibility)
	if err != nil {
		return Activity{}, err
	}

	for _, userID := range in.ParticipantUserIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity_participants (activity_id, user_id, added_by) VALUES ($1,$2,$3)
			ON CONFLICT (activity_id, user_id) DO NOTHING
		`, a.ID, userID, in.CreatedBy); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
				return Activity{}, ErrParticipantNotFound
			}
			return Activity{}, err
		}
	}

	return a, tx.Commit(ctx)
}

func (r *Repository) List(ctx context.Context, limit, offset int, ascending bool) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+activitySelect+`
		ORDER BY a.start_date `+sortDirection(ascending)+` LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var a Activity
		if err := scanActivity(rows, &a); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListActive returns activities that still need attention: any that is not cancelled and
// has not ended yet (it ends on its end_date, included), plus anything (even over or
// cancelled) that still has a rental linked to it that hasn't been returned, or a loan whose
// activity still has some of that item outstanding (every loan minus
// every return, summed per item — a loan's own status is always
// 'completed' the moment it's recorded, so it can't be checked the same
// way a rental's open status is). With activities piling up for years,
// this is what the club actually wants to see day to day — List still
// exists for browsing the full history.
func (r *Repository) ListActive(ctx context.Context, limit, offset int, ascending bool) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT `+activitySelect+`
		LEFT JOIN material_movements t ON t.activity_id = a.id AND t.status IN ('active', 'overdue')
		WHERE (NOT a.cancelled AND a.end_date >= CURRENT_DATE) OR t.id IS NOT NULL OR EXISTS (
			SELECT 1 FROM material_movements m
			WHERE m.activity_id = a.id AND m.type IN ('loan', 'return')
			GROUP BY m.item_id
			HAVING SUM(CASE WHEN m.type = 'loan' THEN m.quantity ELSE -m.quantity END) > 0
		)
		ORDER BY a.start_date `+sortDirection(ascending)+` LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var a Activity
		if err := scanActivity(rows, &a); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListByUser returns every activity where the given person is either the
// leader or a participant, most recent first — the activity history
// shown on that person's page.
func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT `+activitySelect+`
		LEFT JOIN activity_participants ap ON ap.activity_id = a.id
		WHERE a.leader_id = $1 OR ap.user_id = $1
		ORDER BY a.start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var a Activity
		if err := scanActivity(rows, &a); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Activity, error) {
	var a Activity
	err := scanActivity(r.pool.QueryRow(ctx, `
		SELECT `+activitySelect+`
		WHERE a.id = $1
	`, id), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Activity, error) {
	a, err := r.GetByID(ctx, id)
	if err != nil {
		return a, err
	}
	if in.Title != nil {
		a.Title = *in.Title
	}
	if in.Type != nil {
		a.Type = *in.Type
	}
	if in.StartDate != nil {
		a.StartDate = *in.StartDate
	}
	if in.EndDate != nil {
		a.EndDate = *in.EndDate
	}
	if in.Description != nil {
		a.Description = in.Description
	}
	if in.Cancelled != nil {
		a.Cancelled = *in.Cancelled
	}
	if in.Visibility != nil {
		a.Visibility = *in.Visibility
	}
	if in.LeaderID != nil {
		a.LeaderID = *in.LeaderID
	}
	// Checked on the merged values: either date can be sent alone.
	if a.EndDate.Before(a.StartDate) {
		return a, ErrInvalidDateRange
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE activities SET title=$1, type=$2, start_date=$3, end_date=$4, description=$5, cancelled=$6, visibility=$7, leader_id=$8 WHERE id=$9
	`, a.Title, a.Type, a.StartDate, a.EndDate, a.Description, a.Cancelled, a.Visibility, a.LeaderID, id)
	if err != nil {
		return a, err
	}
	if in.LeaderID != nil {
		// a.LeaderLogin still holds the *previous* leader's login — it
		// came from GetByID's JOIN before leader_id changed above.
		if err := r.pool.QueryRow(ctx, `SELECT login FROM users WHERE id = $1`, a.LeaderID).Scan(&a.LeaderLogin); err != nil {
			return a, err
		}
	}
	return a, nil
}

// Delete removes an activity that has nothing attached: nothing cascades, so an activity with
// participants, resources (memoria) or material movements (a loan, a return) is refused
// (ErrActivityInUse) until those are removed, one by one, deliberately.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var locked int64
	err = tx.QueryRow(ctx, `SELECT id FROM activities WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var attached int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM activity_participants WHERE activity_id = $1)
		     + (SELECT count(*) FROM activity_resources WHERE activity_id = $1)
		     + (SELECT count(*) FROM material_movements WHERE activity_id = $1)
	`, id).Scan(&attached); err != nil {
		return err
	}
	if attached > 0 {
		return ErrActivityInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM activities WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddParticipant signs a person up, recording who did it (addedBy: the person themselves, or somebody
// else) and when. Signing up somebody who is already in changes nothing, the first record stays.
func (r *Repository) AddParticipant(ctx context.Context, activityID, userID, addedBy int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO activity_participants (activity_id, user_id, added_by) VALUES ($1,$2,$3)
		ON CONFLICT (activity_id, user_id) DO NOTHING
	`, activityID, userID, addedBy)
	return err
}

// AddedBy is who signed the participant up, and false if they are not signed up.
func (r *Repository) AddedBy(ctx context.Context, activityID, userID int64) (int64, bool, error) {
	var addedBy int64
	err := r.pool.QueryRow(ctx, `
		SELECT added_by FROM activity_participants WHERE activity_id = $1 AND user_id = $2
	`, activityID, userID).Scan(&addedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return addedBy, err == nil, err
}

// RemoveParticipant takes a person off an activity — refused (ErrHasGuests) while they still have
// guests in it: those go first, nothing cascades.
func (r *Repository) RemoveParticipant(ctx context.Context, activityID, userID int64) error {
	// In its own (sub)transaction so a refused delete is rolled back on its own: under test, where
	// every call shares one outer transaction, the violation would otherwise abort all of it.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		DELETE FROM activity_participants WHERE activity_id=$1 AND user_id=$2
	`, activityID, userID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
		return ErrHasGuests
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsParticipant reports whether userID has an actual activity_participants
// row for activityID (any role — Create never sets one explicitly, so
// every row defaults to 'participant', not just the organizer's own).
// The organizer isn't automatically one of these rows at all unless
// their own login was also given as one of the initial
// participant_logins at creation time (see Repository.Create) — a
// caller that needs "attended, organizer included" (internal/
// activityresources's contribution check) compares against
// Activity.LeaderID separately rather than relying on this alone.
func (r *Repository) IsParticipant(ctx context.Context, activityID, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM activity_participants WHERE activity_id = $1 AND user_id = $2)
	`, activityID, userID).Scan(&exists)
	return exists, err
}

func (r *Repository) ListParticipants(ctx context.Context, activityID int64) ([]Participant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ap.activity_id, ap.user_id, u.login, ab.login, ap.added_at
		FROM activity_participants ap
		JOIN users u ON u.id = ap.user_id
		JOIN users ab ON ab.id = ap.added_by
		WHERE ap.activity_id = $1
	`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Participant{}
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.ActivityID, &p.UserID, &p.UserLogin, &p.AddedByLogin, &p.AddedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// ListHolidays returns every holiday in the given year, in date order —
// year <= 0 means every year on file instead. Backs the calendar view;
// there's no per-caller filtering here, holidays aren't sensitive.
func (r *Repository) ListHolidays(ctx context.Context, year int) ([]Holiday, error) {
	var rows pgx.Rows
	var err error
	if year > 0 {
		rows, err = r.pool.Query(ctx, `
			SELECT id, date, name FROM holidays WHERE EXTRACT(YEAR FROM date) = $1 ORDER BY date
		`, year)
	} else {
		rows, err = r.pool.Query(ctx, `SELECT id, date, name FROM holidays ORDER BY date`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Holiday{}
	for rows.Next() {
		var h Holiday
		if err := rows.Scan(&h.ID, &h.Date, &h.Name); err != nil {
			return nil, err
		}
		list = append(list, h)
	}
	return list, rows.Err()
}

func (r *Repository) CreateHoliday(ctx context.Context, date time.Time, name string) (Holiday, error) {
	h := Holiday{Date: date, Name: name}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO holidays (date, name) VALUES ($1,$2) RETURNING id
	`, date, name).Scan(&h.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return Holiday{}, ErrDuplicateHoliday
	}
	return h, err
}

// DeleteHoliday returns the row it removed (via DELETE ... RETURNING,
// atomically — no separate fetch-then-delete race) so the caller can
// still name what was deleted in a mutation notification afterward,
// once there's nothing left to look up.
func (r *Repository) DeleteHoliday(ctx context.Context, id int64) (Holiday, error) {
	var h Holiday
	err := r.pool.QueryRow(ctx, `DELETE FROM holidays WHERE id = $1 RETURNING id, date, name`, id).Scan(&h.ID, &h.Date, &h.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, ErrHolidayNotFound
	}
	return h, err
}

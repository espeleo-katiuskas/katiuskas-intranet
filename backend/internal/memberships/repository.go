package memberships

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("membership not found")
	ErrOverlap  = errors.New("membership period overlaps an existing one for this user")
	// ErrLeaveNotFound and ErrLeaveOverlap mirror ErrNotFound/ErrOverlap,
	// just for membership_leaves instead of memberships.
	ErrLeaveNotFound = errors.New("temporary leave not found")
	ErrLeaveOverlap  = errors.New("temporary leave period overlaps an existing one within this membership")
	// ErrLeaveOutsideMembership means the leave's date range isn't fully
	// contained within its membership period — the one invariant this
	// package has to check itself, since it spans two tables.
	ErrLeaveOutsideMembership = errors.New("temporary leave must fall entirely within the membership period it belongs to")
)

// exclusionViolationCode is the PostgreSQL error code raised when the
// EXCLUDE USING gist constraint on memberships.period rejects an insert.
const exclusionViolationCode = "23P01"

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Membership, error) {
	var m Membership
	err := r.pool.QueryRow(ctx, `
		INSERT INTO memberships (user_id, start_date)
		VALUES ($1,$2)
		RETURNING id, user_id, start_date, end_date, end_reason
	`, in.UserID, in.StartDate,
	).Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

// GetByID is used by Update to merge partial changes onto the existing
// row before writing it back (the same pattern users.Update follows).
func (r *Repository) GetByID(ctx context.Context, id int64) (Membership, error) {
	var m Membership
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, start_date, end_date, end_reason
		FROM memberships WHERE id = $1
	`, id).Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Membership, error) {
	m, err := r.GetByID(ctx, id)
	if err != nil {
		return m, err
	}
	if in.EndReason != nil {
		m.EndReason = in.EndReason
	}
	m.StartDate = in.StartDate
	m.EndDate = in.EndDate

	_, err = r.pool.Exec(ctx, `
		UPDATE memberships SET start_date = $1, end_date = $2, end_reason = $3
		WHERE id = $4
	`, m.StartDate, m.EndDate, m.EndReason, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, start_date, end_date, end_reason
		FROM memberships WHERE user_id = $1 ORDER BY start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate, &m.EndReason); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// IsActiveMember reports whether userID currently has an open membership
// period — used to gate anything that should only apply to someone who
// hasn't caused baja, like assigning a federative license.
func (r *Repository) IsActiveMember(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id = $1 AND period @> CURRENT_DATE)
	`, userID).Scan(&exists)
	return exists, err
}

func (r *Repository) ListActive(ctx context.Context) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.user_id, m.start_date, m.end_date, m.end_reason,
			u.first_name, u.last_name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.period @> CURRENT_DATE
		ORDER BY u.last_name, u.first_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.StartDate, &m.EndDate,
			&m.EndReason, &m.FirstName, &m.LastName); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// withinMembershipPeriod reports whether [start, end] falls entirely
// within m's period — the containment check CreateLeave and UpdateLeave
// run before ever touching the database, since a CHECK constraint can't
// reach across to the memberships table.
func withinMembershipPeriod(m Membership, start, end time.Time) bool {
	if start.Before(m.StartDate) {
		return false
	}
	if m.EndDate != nil && end.After(*m.EndDate) {
		return false
	}
	return true
}

func (r *Repository) CreateLeave(ctx context.Context, in CreateLeaveInput) (Leave, error) {
	m, err := r.GetByID(ctx, in.MembershipID)
	if err != nil {
		return Leave{}, err
	}
	if !withinMembershipPeriod(m, in.StartDate, in.EndDate) {
		return Leave{}, ErrLeaveOutsideMembership
	}

	var l Leave
	err = r.pool.QueryRow(ctx, `
		INSERT INTO membership_leaves (membership_id, start_date, end_date, reason)
		VALUES ($1,$2,$3,$4)
		RETURNING id, membership_id, start_date, end_date, reason
	`, in.MembershipID, in.StartDate, in.EndDate, in.Reason,
	).Scan(&l.ID, &l.MembershipID, &l.StartDate, &l.EndDate, &l.Reason)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return l, ErrLeaveOverlap
	}
	return l, err
}

func (r *Repository) GetLeaveByID(ctx context.Context, id int64) (Leave, error) {
	var l Leave
	err := r.pool.QueryRow(ctx, `
		SELECT id, membership_id, start_date, end_date, reason
		FROM membership_leaves WHERE id = $1
	`, id).Scan(&l.ID, &l.MembershipID, &l.StartDate, &l.EndDate, &l.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrLeaveNotFound
	}
	return l, err
}

// UpdateLeave re-validates containment against the parent membership on
// every correction, not just at creation — an edit could just as easily
// push the leave's dates outside the period as a bad initial input could.
func (r *Repository) UpdateLeave(ctx context.Context, id int64, in UpdateLeaveInput) (Leave, error) {
	l, err := r.GetLeaveByID(ctx, id)
	if err != nil {
		return l, err
	}
	m, err := r.GetByID(ctx, l.MembershipID)
	if err != nil {
		return l, err
	}
	if !withinMembershipPeriod(m, in.StartDate, in.EndDate) {
		return l, ErrLeaveOutsideMembership
	}
	if in.Reason != nil {
		l.Reason = in.Reason
	}
	l.StartDate = in.StartDate
	l.EndDate = in.EndDate

	_, err = r.pool.Exec(ctx, `
		UPDATE membership_leaves SET start_date = $1, end_date = $2, reason = $3
		WHERE id = $4
	`, l.StartDate, l.EndDate, l.Reason, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return l, ErrLeaveOverlap
	}
	return l, err
}

func (r *Repository) DeleteLeave(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM membership_leaves WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaveNotFound
	}
	return nil
}

func (r *Repository) ListLeavesByMembership(ctx context.Context, membershipID int64) ([]Leave, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, membership_id, start_date, end_date, reason
		FROM membership_leaves WHERE membership_id = $1 ORDER BY start_date DESC
	`, membershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Leave{}
	for rows.Next() {
		var l Leave
		if err := rows.Scan(&l.ID, &l.MembershipID, &l.StartDate, &l.EndDate, &l.Reason); err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

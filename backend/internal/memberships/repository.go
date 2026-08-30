package memberships

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("membership not found")
	ErrOverlap  = errors.New("membership period overlaps an existing one for this user")
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
		INSERT INTO memberships (user_id, membership_number, start_date)
		VALUES ($1,$2,$3)
		RETURNING id, user_id, membership_number, start_date, end_date, end_reason
	`, in.UserID, in.MembershipNumber, in.StartDate,
	).Scan(&m.ID, &m.UserID, &m.MembershipNumber, &m.StartDate, &m.EndDate, &m.EndReason)

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
		SELECT id, user_id, membership_number, start_date, end_date, end_reason
		FROM memberships WHERE id = $1
	`, id).Scan(&m.ID, &m.UserID, &m.MembershipNumber, &m.StartDate, &m.EndDate, &m.EndReason)
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
	if in.MembershipNumber != nil {
		m.MembershipNumber = in.MembershipNumber
	}
	if in.EndReason != nil {
		m.EndReason = in.EndReason
	}
	m.StartDate = in.StartDate
	m.EndDate = in.EndDate

	_, err = r.pool.Exec(ctx, `
		UPDATE memberships SET membership_number = $1, start_date = $2, end_date = $3, end_reason = $4
		WHERE id = $5
	`, m.MembershipNumber, m.StartDate, m.EndDate, m.EndReason, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, membership_number, start_date, end_date, end_reason
		FROM memberships WHERE user_id = $1 ORDER BY start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.MembershipNumber, &m.StartDate, &m.EndDate, &m.EndReason); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (r *Repository) ListActive(ctx context.Context) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.user_id, m.membership_number, m.start_date, m.end_date, m.end_reason,
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
		if err := rows.Scan(&m.ID, &m.UserID, &m.MembershipNumber, &m.StartDate, &m.EndDate,
			&m.EndReason, &m.FirstName, &m.LastName); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

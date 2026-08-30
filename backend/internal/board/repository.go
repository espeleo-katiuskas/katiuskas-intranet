package board

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("board term not found")
	// ErrOverlap means this person already has an open (or overlapping)
	// board term — someone can't hold two positions, or the same
	// position twice, at once.
	ErrOverlap = errors.New("board term overlaps an existing one for this person")
)

// exclusionViolationCode is the PostgreSQL error code raised when the
// EXCLUDE USING gist constraint on board_members.period rejects a write.
const exclusionViolationCode = "23P01"

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Member, error) {
	var m Member
	err := r.pool.QueryRow(ctx, `
		INSERT INTO board_members (user_id, position, start_date)
		VALUES ($1,$2,$3)
		RETURNING id, user_id, position, start_date, end_date
	`, in.UserID, in.Position, in.StartDate,
	).Scan(&m.ID, &m.UserID, &m.Position, &m.StartDate, &m.EndDate)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Member, error) {
	var m Member
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, position, start_date, end_date
		FROM board_members WHERE id = $1
	`, id).Scan(&m.ID, &m.UserID, &m.Position, &m.StartDate, &m.EndDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Member, error) {
	m, err := r.GetByID(ctx, id)
	if err != nil {
		return m, err
	}
	m.Position = in.Position
	m.StartDate = in.StartDate
	m.EndDate = in.EndDate

	_, err = r.pool.Exec(ctx, `
		UPDATE board_members SET position = $1, start_date = $2, end_date = $3
		WHERE id = $4
	`, m.Position, m.StartDate, m.EndDate, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode {
		return m, ErrOverlap
	}
	return m, err
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, position, start_date, end_date
		FROM board_members WHERE user_id = $1 ORDER BY start_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.Position, &m.StartDate, &m.EndDate); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// ListActive is the current Junta Directiva: everyone with an open term
// right now, across every position — including email/phone, since this
// is also how a plain member is meant to find out how to reach them.
func (r *Repository) ListActive(ctx context.Context) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bm.id, bm.user_id, u.login, bm.position, bm.start_date, bm.end_date,
			u.first_name, u.last_name, u.email, u.phone
		FROM board_members bm
		JOIN users u ON u.id = bm.user_id
		WHERE bm.period @> CURRENT_DATE
		ORDER BY bm.position, u.last_name, u.first_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.UserLogin, &m.Position, &m.StartDate, &m.EndDate,
			&m.FirstName, &m.LastName, &m.Email, &m.Phone); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

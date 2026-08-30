package licenses

import (
	"context"
	"time"

	"katiuskas-intranet/backend/internal/platform/db"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// validUntil is December 31 of the year issuedDate falls in — a license
// is annual, valid through the end of the calendar year it was issued.
func validUntil(issuedDate time.Time) time.Time {
	return time.Date(issuedDate.Year(), time.December, 31, 0, 0, 0, 0, time.UTC)
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (License, error) {
	var l License
	err := r.pool.QueryRow(ctx, `
		INSERT INTO licenses (user_id, zone, modality, issued_date)
		VALUES ($1,$2,$3,$4)
		RETURNING id, user_id, zone, modality, issued_date, created_at
	`, in.UserID, in.Zone, in.Modality, in.IssuedDate,
	).Scan(&l.ID, &l.UserID, &l.Zone, &l.Modality, &l.IssuedDate, &l.CreatedAt)
	if err != nil {
		return l, err
	}
	l.ValidUntil = validUntil(l.IssuedDate)
	return l, nil
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]License, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, zone, modality, issued_date, created_at
		FROM licenses WHERE user_id = $1 ORDER BY issued_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []License{}
	for rows.Next() {
		var l License
		if err := rows.Scan(&l.ID, &l.UserID, &l.Zone, &l.Modality, &l.IssuedDate, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.ValidUntil = validUntil(l.IssuedDate)
		list = append(list, l)
	}
	return list, rows.Err()
}

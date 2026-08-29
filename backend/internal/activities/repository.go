package activities

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/platform/db"
)

var ErrNotFound = errors.New("activity not found")

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Activity, error) {
	var a Activity
	err := r.pool.QueryRow(ctx, `
		INSERT INTO activities (title, type, start_date, end_date, location, description,
			difficulty, max_participants, leader_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, title, type, start_date, end_date, location, description,
			difficulty, max_participants, status, leader_id
	`, in.Title, in.Type, in.StartDate, in.EndDate, in.Location, in.Description,
		in.Difficulty, in.MaxParticipants, in.LeaderID,
	).Scan(&a.ID, &a.Title, &a.Type, &a.StartDate, &a.EndDate, &a.Location, &a.Description,
		&a.Difficulty, &a.MaxParticipants, &a.Status, &a.LeaderID)
	return a, err
}

func (r *Repository) List(ctx context.Context, limit, offset int) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, type, start_date, end_date, location, description,
			difficulty, max_participants, status, leader_id
		FROM activities ORDER BY start_date DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Title, &a.Type, &a.StartDate, &a.EndDate, &a.Location,
			&a.Description, &a.Difficulty, &a.MaxParticipants, &a.Status, &a.LeaderID); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Activity, error) {
	var a Activity
	err := r.pool.QueryRow(ctx, `
		SELECT id, title, type, start_date, end_date, location, description,
			difficulty, max_participants, status, leader_id
		FROM activities WHERE id = $1
	`, id).Scan(&a.ID, &a.Title, &a.Type, &a.StartDate, &a.EndDate, &a.Location,
		&a.Description, &a.Difficulty, &a.MaxParticipants, &a.Status, &a.LeaderID)
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
	if in.Description != nil {
		a.Description = in.Description
	}
	if in.Status != nil {
		a.Status = *in.Status
	}
	if in.Location != nil {
		a.Location = *in.Location
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE activities SET title=$1, description=$2, status=$3, location=$4 WHERE id=$5
	`, a.Title, a.Description, a.Status, a.Location, id)
	return a, err
}

func (r *Repository) Cancel(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE activities SET status = 'cancelled' WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) AddParticipant(ctx context.Context, activityID, userID int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO activity_participants (activity_id, user_id) VALUES ($1,$2)
		ON CONFLICT (activity_id, user_id) DO NOTHING
	`, activityID, userID)
	return err
}

func (r *Repository) RemoveParticipant(ctx context.Context, activityID, userID int64) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM activity_participants WHERE activity_id=$1 AND user_id=$2
	`, activityID, userID)
	return err
}

func (r *Repository) ListParticipants(ctx context.Context, activityID int64) ([]Participant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT activity_id, user_id, role, confirmed FROM activity_participants WHERE activity_id = $1
	`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Participant
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.ActivityID, &p.UserID, &p.Role, &p.Confirmed); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

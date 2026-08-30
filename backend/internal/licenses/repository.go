package licenses

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	// ErrTypeNotFound means the given license_type_id isn't in the catalog.
	ErrTypeNotFound = errors.New("license type not found")
	// ErrNotFound means the given license id doesn't exist.
	ErrNotFound = errors.New("license not found")
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

func displayType(name string, otherDetail *string) string {
	if otherDetail != nil {
		return name + ": " + *otherDetail
	}
	return name
}

func (r *Repository) ListTypes(ctx context.Context) ([]LicenseType, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, is_other FROM license_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []LicenseType{}
	for rows.Next() {
		var lt LicenseType
		if err := rows.Scan(&lt.ID, &lt.Name, &lt.IsOther); err != nil {
			return nil, err
		}
		list = append(list, lt)
	}
	return list, rows.Err()
}

// GetTypeByID is used by Service.Create to check whether the chosen
// type is the "Otra" escape hatch, which requires OtherDetail.
func (r *Repository) GetTypeByID(ctx context.Context, id int64) (LicenseType, error) {
	var lt LicenseType
	err := r.pool.QueryRow(ctx, `SELECT id, name, is_other FROM license_types WHERE id = $1`, id).
		Scan(&lt.ID, &lt.Name, &lt.IsOther)
	if errors.Is(err, pgx.ErrNoRows) {
		return lt, ErrTypeNotFound
	}
	return lt, err
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (License, error) {
	var l License
	err := r.pool.QueryRow(ctx, `
		INSERT INTO licenses (user_id, license_type_id, other_detail, issued_date)
		VALUES ($1,$2,$3,$4)
		RETURNING id, user_id, license_type_id, other_detail, issued_date, created_at
	`, in.UserID, in.LicenseTypeID, in.OtherDetail, in.IssuedDate,
	).Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &l.OtherDetail, &l.IssuedDate, &l.CreatedAt)
	if err != nil {
		return l, err
	}
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT name FROM license_types WHERE id = $1`, l.LicenseTypeID).Scan(&name); err != nil {
		return l, err
	}
	l.Type = displayType(name, l.OtherDetail)
	l.ValidUntil = validUntil(l.IssuedDate)
	return l, nil
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (License, error) {
	var l License
	err := r.pool.QueryRow(ctx, `
		UPDATE licenses SET license_type_id=$1, other_detail=$2, issued_date=$3
		WHERE id=$4
		RETURNING id, user_id, license_type_id, other_detail, issued_date, created_at
	`, in.LicenseTypeID, in.OtherDetail, in.IssuedDate, id,
	).Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &l.OtherDetail, &l.IssuedDate, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT name FROM license_types WHERE id = $1`, l.LicenseTypeID).Scan(&name); err != nil {
		return l, err
	}
	l.Type = displayType(name, l.OtherDetail)
	l.ValidUntil = validUntil(l.IssuedDate)
	return l, nil
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM licenses WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]License, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.user_id, l.license_type_id, lt.name, l.other_detail, l.issued_date, l.created_at
		FROM licenses l JOIN license_types lt ON lt.id = l.license_type_id
		WHERE l.user_id = $1 ORDER BY l.issued_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []License{}
	for rows.Next() {
		var l License
		var name string
		if err := rows.Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &name, &l.OtherDetail, &l.IssuedDate, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Type = displayType(name, l.OtherDetail)
		l.ValidUntil = validUntil(l.IssuedDate)
		list = append(list, l)
	}
	return list, rows.Err()
}

package licenses

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	// ErrTypeNotFound means the given license_type_id isn't in the catalog.
	ErrTypeNotFound = errors.New("license type not found")
	// ErrNotFound means the given license id doesn't exist.
	ErrNotFound = errors.New("license not found")
	// ErrUnknownTransaction means the given transaction_id doesn't name
	// an existing transaction — every license has to be tied to the
	// payment that recorded it (migration
	// 00014_membership_dues_and_license_payments.sql), so unlike
	// OtherDetail/IssuedDate this can't just be left off.
	ErrUnknownTransaction = errors.New("transaction_id does not name an existing transaction")
	// ErrTransactionNotForLicenses means the transaction exists but none
	// of its entries is posted to an account flagged allows_licenses
	// (migration 00017_accounts_dues_licenses_flags.sql) — only a
	// transaction that touches such an account can be the payment behind
	// a new license.
	ErrTransactionNotForLicenses = errors.New("transaction has no entry on an account that accepts licenses")
)

const foreignKeyViolationCode = "23503"

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// validUntil is December 31 of the license's own Year — a license is
// annual, valid through the end of whichever calendar year it actually
// covers (not necessarily the year it was issued in — see the package
// doc comment).
func validUntil(year int) time.Time {
	return time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
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

// TransactionAllowsLicenses reports whether transactionID exists and, if
// it does, whether at least one of its entries is posted to an account
// flagged allows_licenses. Answered in SQL against accounting's own
// tables — the same way a license already references its transaction by
// foreign key — rather than by importing internal/accounting.
func (r *Repository) TransactionAllowsLicenses(ctx context.Context, transactionID int64) (exists, allowed bool, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM transactions WHERE id = $1),
			EXISTS (
				SELECT 1 FROM transaction_entries te
				JOIN accounts a ON a.id = te.account_id
				WHERE te.transaction_id = $1 AND a.allows_licenses
			)
	`, transactionID).Scan(&exists, &allowed)
	return exists, allowed, err
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (License, error) {
	var l License
	err := r.pool.QueryRow(ctx, `
		INSERT INTO licenses (user_id, license_type_id, other_detail, issued_date, year, transaction_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, user_id, license_type_id, other_detail, issued_date, year, transaction_id, created_at
	`, in.UserID, in.LicenseTypeID, in.OtherDetail, in.IssuedDate, in.Year, in.TransactionID,
	).Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &l.OtherDetail, &l.IssuedDate, &l.Year, &l.TransactionID, &l.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
			return l, ErrUnknownTransaction
		}
		return l, err
	}
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT name FROM license_types WHERE id = $1`, l.LicenseTypeID).Scan(&name); err != nil {
		return l, err
	}
	l.Type = displayType(name, l.OtherDetail)
	l.ValidUntil = validUntil(l.Year)
	return l, nil
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (License, error) {
	var l License
	err := r.pool.QueryRow(ctx, `
		UPDATE licenses SET license_type_id=$1, other_detail=$2, issued_date=$3, year=$4
		WHERE id=$5
		RETURNING id, user_id, license_type_id, other_detail, issued_date, year, transaction_id, created_at
	`, in.LicenseTypeID, in.OtherDetail, in.IssuedDate, in.Year, id,
	).Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &l.OtherDetail, &l.IssuedDate, &l.Year, &l.TransactionID, &l.CreatedAt)
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
	l.ValidUntil = validUntil(l.Year)
	return l, nil
}

// GetByID returns a single license by id, joined with its type name the
// same way ListByUser is — used by Handler's own mutation-notification
// before/after diff on Update and to name what's being removed on
// Delete, not by any route of its own.
func (r *Repository) GetByID(ctx context.Context, id int64) (License, error) {
	var l License
	var name string
	err := r.pool.QueryRow(ctx, `
		SELECT l.id, l.user_id, l.license_type_id, lt.name, l.other_detail, l.issued_date, l.year, l.transaction_id, l.created_at
		FROM licenses l JOIN license_types lt ON lt.id = l.license_type_id
		WHERE l.id = $1
	`, id).Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &name, &l.OtherDetail, &l.IssuedDate, &l.Year, &l.TransactionID, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Type = displayType(name, l.OtherDetail)
	l.ValidUntil = validUntil(l.Year)
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

// ListByTransaction returns every license recorded against one
// transaction — a single payment can cover several socios' licenses —
// each with its owner's login (joined here, unlike every other query in
// this package, since nobody has a single known /users/{login} to fill
// it in from), ordered by that person's name.
func (r *Repository) ListByTransaction(ctx context.Context, transactionID int64) ([]License, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.user_id, u.login, l.license_type_id, lt.name, l.other_detail, l.issued_date, l.year, l.transaction_id, l.created_at
		FROM licenses l
		JOIN users u ON u.id = l.user_id
		JOIN license_types lt ON lt.id = l.license_type_id
		WHERE l.transaction_id = $1
		ORDER BY u.first_name, u.last_name, l.issued_date
	`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []License{}
	for rows.Next() {
		var l License
		var name string
		if err := rows.Scan(&l.ID, &l.UserID, &l.UserLogin, &l.LicenseTypeID, &name, &l.OtherDetail, &l.IssuedDate, &l.Year, &l.TransactionID, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Type = displayType(name, l.OtherDetail)
		l.ValidUntil = validUntil(l.Year)
		list = append(list, l)
	}
	return list, rows.Err()
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]License, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.user_id, l.license_type_id, lt.name, l.other_detail, l.issued_date, l.year, l.transaction_id, l.created_at
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
		if err := rows.Scan(&l.ID, &l.UserID, &l.LicenseTypeID, &name, &l.OtherDetail, &l.IssuedDate, &l.Year, &l.TransactionID, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Type = displayType(name, l.OtherDetail)
		l.ValidUntil = validUntil(l.Year)
		list = append(list, l)
	}
	return list, rows.Err()
}

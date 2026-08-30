package users

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("user not found")
	// ErrHasReferences means the user can't be deleted because other
	// records (an activity they led, an inventory transaction they're
	// party to) still point at them — deleting would silently erase that
	// history, so PostgreSQL's foreign keys refuse the delete instead of
	// cascading through it.
	ErrHasReferences = errors.New("cannot delete: this person still has related activities or transactions")
	// ErrDuplicate means the email or national ID being set already
	// belongs to another person.
	ErrDuplicate = errors.New("email or national ID already belongs to another person")
	// ErrInvalidToken means a magic-link token doesn't exist, already got
	// used, or expired. It's intentionally the same error for all three
	// cases — there's nothing useful (or safe) to tell the caller apart.
	ErrInvalidToken = errors.New("invalid or expired token")
)

const (
	foreignKeyViolationCode = "23503"
	uniqueViolationCode     = "23505"
)

// hasAccessSelect is appended to every query that returns a User: whether
// they have an accounts row at all. Role names aren't selected here —
// "member"/"former_member" are computed from membership history rather
// than stored, so the roles package (roles.Service.ListNamesForUser) is
// the single place that knows how to list a person's roles; the users
// service overlays that onto every User it returns instead of
// duplicating the computation in SQL here.
const hasAccessSelect = `EXISTS(SELECT 1 FROM accounts a WHERE a.user_id = u.id) AS has_access`

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (User, error) {
	var u User
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO users (first_name, last_name, national_id, birth_date, email, phone,
			address, postal_code, city, province, emergency_contact)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, first_name, last_name, national_id, birth_date, email, phone,
			address, postal_code, city, province, emergency_contact, created_at
	`, in.FirstName, in.LastName, in.NationalID, in.BirthDate, in.Email, in.Phone,
		in.Address, in.PostalCode, in.City, in.Province, in.EmergencyContact,
	).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province, &u.EmergencyContact, &u.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return u, ErrDuplicate
	}
	if err != nil {
		return u, err
	}

	if in.GrantAccess {
		if _, err = tx.Exec(ctx, `INSERT INTO accounts (user_id) VALUES ($1)`, u.ID); err != nil {
			return u, err
		}
		u.HasAccess = true
	}

	return u, tx.Commit(ctx)
}

func (r *Repository) GetByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.first_name, u.last_name, u.national_id, u.birth_date, u.email, u.phone,
			u.address, u.postal_code, u.city, u.province, u.emergency_contact, u.created_at,
			`+hasAccessSelect+`
		FROM users u
		WHERE u.id = $1
	`, id).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province, &u.EmergencyContact,
		&u.CreatedAt, &u.HasAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) GetByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.first_name, u.last_name, u.national_id, u.birth_date, u.email, u.phone,
			u.address, u.postal_code, u.city, u.province, u.emergency_contact, u.created_at,
			`+hasAccessSelect+`
		FROM users u
		WHERE u.email = $1
	`, email).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province, &u.EmergencyContact,
		&u.CreatedAt, &u.HasAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) List(ctx context.Context, limit, offset int) ([]User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.first_name, u.last_name, u.national_id, u.birth_date, u.email, u.phone,
			u.address, u.postal_code, u.city, u.province, u.emergency_contact, u.created_at,
			`+hasAccessSelect+`
		FROM users u
		ORDER BY u.last_name, u.first_name LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate,
			&u.Email, &u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province,
			&u.EmergencyContact, &u.CreatedAt, &u.HasAccess); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// FindActiveByEmailAndPhone is the login lookup: matches an active
// account by email and phone (digits only, so formatting differences
// don't matter), or ErrNotFound for absolutely any other reason — wrong
// phone, unknown email, no account, or a deactivated one. The caller must
// not distinguish between those cases in its response.
func (r *Repository) FindActiveByEmailAndPhone(ctx context.Context, email, phone string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.first_name, u.last_name, u.national_id, u.birth_date, u.email, u.phone,
			u.address, u.postal_code, u.city, u.province, u.emergency_contact, u.created_at,
			`+hasAccessSelect+`
		FROM users u
		JOIN accounts a ON a.user_id = u.id AND a.is_active
		WHERE u.email = $1
			AND u.phone IS NOT NULL
			AND regexp_replace(u.phone, '\D', '', 'g') = regexp_replace($2, '\D', '', 'g')
			AND regexp_replace($2, '\D', '', 'g') <> ''
	`, email, phone).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province, &u.EmergencyContact,
		&u.CreatedAt, &u.HasAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (User, error) {
	u, err := r.GetByID(ctx, id)
	if err != nil {
		return u, err
	}
	if in.FirstName != nil {
		u.FirstName = *in.FirstName
	}
	if in.LastName != nil {
		u.LastName = *in.LastName
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.NationalID != nil {
		u.NationalID = in.NationalID
	}
	if in.BirthDate != nil {
		u.BirthDate = in.BirthDate
	}
	if in.Phone != nil {
		u.Phone = in.Phone
	}
	if in.Address != nil {
		u.Address = in.Address
	}
	if in.PostalCode != nil {
		u.PostalCode = in.PostalCode
	}
	if in.City != nil {
		u.City = in.City
	}
	if in.Province != nil {
		u.Province = in.Province
	}
	if in.EmergencyContact != nil {
		u.EmergencyContact = in.EmergencyContact
	}

	_, err = r.pool.Exec(ctx, `
		UPDATE users SET first_name=$1, last_name=$2, email=$3, national_id=$4, birth_date=$5,
			phone=$6, address=$7, postal_code=$8, city=$9, province=$10, emergency_contact=$11,
			updated_at=now()
		WHERE id=$12
	`, u.FirstName, u.LastName, u.Email, u.NationalID, u.BirthDate, u.Phone, u.Address,
		u.PostalCode, u.City, u.Province, u.EmergencyContact, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return u, ErrDuplicate
	}
	return u, err
}

// Delete removes a person's record entirely, along with their account,
// refresh tokens, magic-link tokens, membership history and activity
// sign-ups (all ON DELETE CASCADE). It's rejected by the database if the
// person is still referenced as an activity's leader or an inventory
// transaction's party — that history has to be reassigned or completed
// first.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
		return ErrHasReferences
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDirectory returns every person who can sign in (has an accounts
// row), for populating pickers like an activity's participant list.
// People without access aren't club members with an active presence, so
// they're left out.
func (r *Repository) ListDirectory(ctx context.Context) ([]DirectoryEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.first_name, u.last_name
		FROM users u
		JOIN accounts a ON a.user_id = u.id
		ORDER BY u.last_name, u.first_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []DirectoryEntry{}
	for rows.Next() {
		var e DirectoryEntry
		if err := rows.Scan(&e.ID, &e.FirstName, &e.LastName); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

func (r *Repository) GetAccountByUserID(ctx context.Context, userID int64) (Account, error) {
	var a Account
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, is_active FROM accounts WHERE user_id = $1
	`, userID).Scan(&a.UserID, &a.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (r *Repository) TouchLastLogin(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE accounts SET last_login_at = now() WHERE user_id = $1`, userID)
	return err
}

func (r *Repository) SaveRefreshToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1,$2,$3)
	`, userID, tokenHash, expiresAt)
	return err
}

func (r *Repository) GetRefreshToken(ctx context.Context, tokenHash string) (userID int64, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT user_id FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
	`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return userID, err
}

func (r *Repository) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1`, tokenHash)
	return err
}

// CreateMagicLinkToken stores the hash of a freshly issued sign-in link.
func (r *Repository) CreateMagicLinkToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO magic_link_tokens (user_id, token_hash, expires_at) VALUES ($1,$2,$3)
	`, userID, tokenHash, expiresAt)
	return err
}

// ConsumeMagicLinkToken atomically marks a token used and returns whose it
// was, or ErrInvalidToken if it doesn't exist, already got used, or
// expired — so a token can never be replayed.
func (r *Repository) ConsumeMagicLinkToken(ctx context.Context, tokenHash string) (int64, error) {
	var userID int64
	err := r.pool.QueryRow(ctx, `
		UPDATE magic_link_tokens SET consumed_at = now()
		WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
		RETURNING user_id
	`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInvalidToken
	}
	return userID, err
}

// HasRecentMagicLinkToken reports whether userID already requested a link
// within the cooldown window, so a repeated request doesn't spam their
// inbox (or let someone else spam it).
func (r *Repository) HasRecentMagicLinkToken(ctx context.Context, userID int64, since time.Time) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM magic_link_tokens WHERE user_id = $1 AND created_at > $2
		)
	`, userID, since).Scan(&exists)
	return exists, err
}

package users

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("user not found")

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, in CreateInput, passwordHash string) (User, error) {
	var u User
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO users (first_name, last_name, national_id, birth_date, email, phone,
			address, emergency_contact_name, emergency_contact_phone)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, first_name, last_name, national_id, birth_date, email, phone,
			address, emergency_contact_name, emergency_contact_phone, created_at
	`, in.FirstName, in.LastName, in.NationalID, in.BirthDate, in.Email, in.Phone,
		in.Address, in.EmergencyContactName, in.EmergencyContactPhone,
	).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.EmergencyContactName, &u.EmergencyContactPhone, &u.CreatedAt)
	if err != nil {
		return u, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO credentials (user_id, username, password_hash, role)
		VALUES ($1,$2,$3,$4)
	`, u.ID, in.Username, passwordHash, in.Role)
	if err != nil {
		return u, err
	}

	return u, tx.Commit(ctx)
}

func (r *Repository) GetByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		SELECT id, first_name, last_name, national_id, birth_date, email, phone,
			address, emergency_contact_name, emergency_contact_phone, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Email,
		&u.Phone, &u.Address, &u.EmergencyContactName, &u.EmergencyContactPhone, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) List(ctx context.Context, limit, offset int) ([]User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, first_name, last_name, national_id, birth_date, email, phone,
			address, emergency_contact_name, emergency_contact_phone, created_at
		FROM users ORDER BY last_name, first_name LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate,
			&u.Email, &u.Phone, &u.Address, &u.EmergencyContactName,
			&u.EmergencyContactPhone, &u.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
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
	if in.Phone != nil {
		u.Phone = in.Phone
	}
	if in.Address != nil {
		u.Address = in.Address
	}
	if in.EmergencyContactName != nil {
		u.EmergencyContactName = in.EmergencyContactName
	}
	if in.EmergencyContactPhone != nil {
		u.EmergencyContactPhone = in.EmergencyContactPhone
	}

	_, err = r.pool.Exec(ctx, `
		UPDATE users SET first_name=$1, last_name=$2, phone=$3, address=$4,
			emergency_contact_name=$5, emergency_contact_phone=$6, updated_at=now()
		WHERE id=$7
	`, u.FirstName, u.LastName, u.Phone, u.Address, u.EmergencyContactName,
		u.EmergencyContactPhone, id)
	return u, err
}

func (r *Repository) GetCredentialsByUsername(ctx context.Context, username string) (Credentials, error) {
	var c Credentials
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, username, password_hash, role, is_active
		FROM credentials WHERE username = $1
	`, username).Scan(&c.UserID, &c.Username, &c.PasswordHash, &c.Role, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (r *Repository) GetCredentialsByUserID(ctx context.Context, userID int64) (Credentials, error) {
	var c Credentials
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, username, password_hash, role, is_active
		FROM credentials WHERE user_id = $1
	`, userID).Scan(&c.UserID, &c.Username, &c.PasswordHash, &c.Role, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (r *Repository) TouchLastLogin(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE credentials SET last_login_at = now() WHERE user_id = $1`, userID)
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

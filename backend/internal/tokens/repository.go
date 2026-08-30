package tokens

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/platform/db"
)

var ErrNotFound = errors.New("token not found")

const tokenSelect = `id, user_id, created_at, expires_at, revoked_at, last_used_at, last_used_ip, impersonated_by`

func scanToken(row interface{ Scan(dest ...any) error }, t *Token) error {
	return row.Scan(&t.ID, &t.UserID, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.LastUsedIP, &t.ImpersonatedBy)
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (Token, error) {
	var t Token
	err := scanToken(r.pool.QueryRow(ctx, `
		INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)
		RETURNING `+tokenSelect, userID, tokenHash, expiresAt), &t)
	return t, err
}

// CreateImpersonation is Create plus impersonatorID recorded alongside —
// the only difference between an impersonation session and a normal one
// at the database level.
func (r *Repository) CreateImpersonation(ctx context.Context, targetUserID, impersonatorID int64, tokenHash string, expiresAt time.Time) (Token, error) {
	var t Token
	err := scanToken(r.pool.QueryRow(ctx, `
		INSERT INTO tokens (user_id, token_hash, expires_at, impersonated_by) VALUES ($1, $2, $3, $4)
		RETURNING `+tokenSelect, targetUserID, tokenHash, expiresAt, impersonatorID), &t)
	return t, err
}

// Authenticate validates a token hash and, in the same statement, records
// that it was just used — so a session's last_used_at/last_used_ip always
// reflects the request that's checking it, not a separate follow-up
// write.
func (r *Repository) Authenticate(ctx context.Context, tokenHash, ip string) (userID int64, impersonatedBy *int64, err error) {
	var ipArg any
	if ip != "" {
		ipArg = ip
	}
	err = r.pool.QueryRow(ctx, `
		UPDATE tokens SET last_used_at = now(), last_used_ip = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id, impersonated_by
	`, tokenHash, ipArg).Scan(&userID, &impersonatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	return userID, impersonatedBy, err
}

// RevokeByHash is used by logout, which only ever has the raw token, not
// its id.
func (r *Repository) RevokeByHash(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Token, error) {
	var t Token
	err := scanToken(r.pool.QueryRow(ctx, `SELECT `+tokenSelect+` FROM tokens WHERE id = $1`, id), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// ListForUser includes expired and revoked tokens too — this is the
// admin-facing audit view, so the full history is the point. Orders by id,
// not created_at: two tokens issued in the same transaction (as tests
// often do) get an identical created_at, since now() returns the
// transaction's start time rather than the statement's, but id is always
// monotonically increasing.
func (r *Repository) ListForUser(ctx context.Context, userID int64) ([]Token, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+tokenSelect+` FROM tokens WHERE user_id = $1 ORDER BY id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Token
	for rows.Next() {
		var t Token
		if err := scanToken(rows, &t); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) UpdateExpiry(ctx context.Context, id int64, expiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE tokens SET expires_at = $2 WHERE id = $1`, id, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Revoke is idempotent: revoking an already-revoked token is not an
// error, only revoking one that doesn't exist at all is.
func (r *Repository) Revoke(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE tokens SET revoked_at = COALESCE(revoked_at, now()) WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

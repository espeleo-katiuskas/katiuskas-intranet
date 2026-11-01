package tokens

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var ErrNotFound = errors.New("token not found")

// ErrInvalidLifetime means a session was asked for with a lifetime that is not positive.
var ErrInvalidLifetime = errors.New("a session needs a positive lifetime")

// tokenColumns is for INSERT ... RETURNING, which can only return columns
// of the row just written — no JOIN available — so it leaves UserLogin
// and ImpersonatedByLogin blank. That's fine: Create and
// CreateImpersonation's callers only ever check the returned error.
const tokenColumns = `id, user_id, created_at, expires_at, revoked_at, last_used_at, last_used_ip, last_used_user_agent, impersonated_by`

func scanToken(row interface{ Scan(dest ...any) error }, t *Token) error {
	if err := row.Scan(&t.ID, &t.UserID, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.LastUsedIP, &t.LastUsedUserAgent, &t.ImpersonatedBy); err != nil {
		return err
	}
	t.fillClient()
	return nil
}

// tokenSelectJoined is for reads that actually get serialized back over
// the API: it joins in both the token owner's login and, if any, the
// impersonating admin's — the only two identifiers this package's JSON
// is allowed to expose for a person (see Token.UserLogin).
const tokenSelectJoined = `
	t.id, t.user_id, u.login, t.created_at, t.expires_at, t.revoked_at, t.last_used_at, t.last_used_ip, t.last_used_user_agent,
	t.impersonated_by, ib.login
	FROM tokens t
	JOIN users u ON u.id = t.user_id
	LEFT JOIN users ib ON ib.id = t.impersonated_by`

func scanTokenJoined(row interface{ Scan(dest ...any) error }, t *Token) error {
	if err := row.Scan(&t.ID, &t.UserID, &t.UserLogin, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.LastUsedIP,
		&t.LastUsedUserAgent, &t.ImpersonatedBy, &t.ImpersonatedByLogin); err != nil {
		return err
	}
	t.fillClient()
	return nil
}

// fillClient works out the browser and operating system from the stored User-Agent.
func (t *Token) fillClient() {
	if t.LastUsedUserAgent != nil {
		t.Browser, t.OS = DetectClientFromUA(*t.LastUsedUserAgent)
	}
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
		RETURNING `+tokenColumns, userID, tokenHash, expiresAt), &t)
	return t, err
}

// CreateSliding creates a token that expires lifetime after its last use: expires_at starts at now()
// plus lifetime, and every use (Authenticate) moves it to now() plus lifetime again. Create, by
// contrast, makes one with a fixed expiry that uses never move.
func (r *Repository) CreateSliding(ctx context.Context, userID int64, tokenHash string, lifetime time.Duration) (Token, error) {
	var t Token
	err := scanToken(r.pool.QueryRow(ctx, `
		INSERT INTO tokens (user_id, token_hash, expires_at, lifetime)
		VALUES ($1, $2, now() + make_interval(secs => $3::float8), make_interval(secs => $3::float8))
		RETURNING `+tokenColumns, userID, tokenHash, lifetime.Seconds()), &t)
	return t, err
}

// CreateImpersonation is Create plus impersonatorID recorded alongside —
// the only difference between an impersonation session and a normal one
// at the database level.
func (r *Repository) CreateImpersonation(ctx context.Context, targetUserID, impersonatorID int64, tokenHash string, expiresAt time.Time) (Token, error) {
	var t Token
	err := scanToken(r.pool.QueryRow(ctx, `
		INSERT INTO tokens (user_id, token_hash, expires_at, impersonated_by) VALUES ($1, $2, $3, $4)
		RETURNING `+tokenColumns, targetUserID, tokenHash, expiresAt, impersonatorID), &t)
	return t, err
}

// Authenticate validates a token hash and, in the same statement, records
// that it was just used — so a session's last_used_at/last_used_ip/
// last_used_user_agent always reflect the request that's checking it, not a
// separate follow-up write. A token with a lifetime (see CreateSliding) also gets
// its expiry moved to now() plus that lifetime, in the same statement; one without
// keeps the expiry it had. An expired or revoked token matches nothing, so a use can
// never bring one back. The browser is the *last* one, like the address
// it goes with: they describe the same request, so they can be read together.
func (r *Repository) Authenticate(ctx context.Context, tokenHash, ip, userAgent string) (userID int64, impersonatedBy *int64, err error) {
	var ipArg, userAgentArg any
	if ip != "" {
		ipArg = ip
	}
	if userAgent != "" {
		userAgentArg = truncateUserAgent(userAgent)
	}
	err = r.pool.QueryRow(ctx, `
		UPDATE tokens SET last_used_at = now(), last_used_ip = $2, last_used_user_agent = $3,
			expires_at = CASE WHEN lifetime IS NULL THEN expires_at ELSE now() + lifetime END
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id, impersonated_by
	`, tokenHash, ipArg, userAgentArg).Scan(&userID, &impersonatedBy)
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
	err := scanTokenJoined(r.pool.QueryRow(ctx, `SELECT `+tokenSelectJoined+` WHERE t.id = $1`, id), &t)
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
		SELECT `+tokenSelectJoined+` WHERE t.user_id = $1 ORDER BY t.id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Token
	for rows.Next() {
		var t Token
		if err := scanTokenJoined(rows, &t); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// ExpiresAtByHash is when the token with that hash expires, or ErrNotFound.
func (r *Repository) ExpiresAtByHash(ctx context.Context, tokenHash string) (time.Time, error) {
	var at time.Time
	err := r.pool.QueryRow(ctx, `SELECT expires_at FROM tokens WHERE token_hash = $1`, tokenHash).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return at, err
}

// IDByHash is the id of the token with that hash, or ErrNotFound.
func (r *Repository) IDByHash(ctx context.Context, tokenHash string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM tokens WHERE token_hash = $1`, tokenHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// ListAll lists the tokens of everyone, most recently used first (never
// used ones last), a page at a time — the admin's view of who is signed in
// (activeOnly: not revoked and not expired yet) or of the whole history.
func (r *Repository) ListAll(ctx context.Context, activeOnly bool, limit, offset int) ([]Token, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+tokenSelectJoined+`
		WHERE (NOT $1 OR (t.revoked_at IS NULL AND t.expires_at > now()))
		ORDER BY t.last_used_at DESC NULLS LAST, t.id DESC
		LIMIT $2 OFFSET $3
	`, activeOnly, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Token{}
	for rows.Next() {
		var t Token
		if err := scanTokenJoined(rows, &t); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) UpdateExpiry(ctx context.Context, id int64, expiresAt time.Time) error {
	// An admin who sets when a session ends is also setting how long it lasts from now on: its
	// lifetime becomes the time from now to that moment, so that the next use does not undo the
	// change (which would make shortening a session in use impossible). A moment already past
	// leaves the lifetime alone — the session is over either way — and so does a token with a
	// fixed expiry, which has none.
	tag, err := r.pool.Exec(ctx, `
		UPDATE tokens SET expires_at = $2,
			lifetime = CASE WHEN lifetime IS NOT NULL AND $2 > now() THEN $2 - now() ELSE lifetime END
		WHERE id = $1`, id, expiresAt)
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

package users

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("user not found")
	// ErrHasReferences means the user can't be deleted because other
	// records (an activity they led, an inventory transaction they're
	// party to) still point at them — deleting would silently erase that
	// history, so PostgreSQL's foreign keys refuse the delete instead of
	// cascading through it.
	ErrHasReferences = errors.New("cannot delete: this person still has related activities or transactions")
	// ErrDuplicate means an email (or one of several), the national ID,
	// or the membership number being set already belongs to another
	// person.
	ErrDuplicate = errors.New("email, national ID, login, or membership number already belongs to another person")
	// ErrInvalidLogin means an admin-supplied login doesn't match
	// loginFormat — lowercase letters, digits, dots, and hyphens only,
	// never at the edges or doubled.
	ErrInvalidLogin = errors.New("login must be lowercase letters, digits, dots, or hyphens")
	// ErrInvalidToken means a magic-link token doesn't exist, already got
	// used, or expired. It's intentionally the same error for all three
	// cases — there's nothing useful (or safe) to tell the caller apart.
	ErrInvalidToken = errors.New("invalid or expired token")
)

const (
	foreignKeyViolationCode = "23503"
	uniqueViolationCode     = "23505"
)

// userSelect is appended to every query that returns a User. Role names
// aren't selected here — "member"/"former_member"/"board" are computed
// from membership/board history rather than stored, so the roles package
// (roles.Service.ListNamesForUser) is the single place that knows how
// to list a person's roles; the users service overlays that onto every
// User it returns instead of duplicating the computation in SQL here.
const userSelect = `
	u.id, u.login, u.first_name, u.last_name, u.national_id, u.birth_date, u.gender, u.email, u.phone,
	u.address, u.postal_code, u.city, u.province, u.emergency_contact, u.membership_number,
	u.last_login_at, u.last_login_ip, u.created_at`

func scanUser(row interface{ Scan(dest ...any) error }, u *User) error {
	return row.Scan(&u.ID, &u.Login, &u.FirstName, &u.LastName, &u.NationalID, &u.BirthDate, &u.Gender, &u.Email,
		&u.Phone, &u.Address, &u.PostalCode, &u.City, &u.Province, &u.EmergencyContact, &u.MembershipNumber,
		&u.LastLoginAt, &u.LastLoginIP, &u.CreatedAt)
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// emailTakenByAnotherUser reports whether any of the space-separated
// emails in candidate already belongs to a user other than excludeID
// (pass 0 when checking a not-yet-created person). This is the check a
// plain UNIQUE constraint used to do before email could hold more than
// one address — Postgres can't express "no two rows share an element"
// as a column constraint, so it's enforced here instead.
func (r *Repository) emailTakenByAnotherUser(ctx context.Context, excludeID int64, candidate string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id <> $1
				AND string_to_array(email, ' ') && string_to_array($2, ' ')
		)
	`, excludeID, candidate).Scan(&exists)
	return exists, err
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (User, error) {
	if in.Email != nil {
		if taken, err := r.emailTakenByAnotherUser(ctx, 0, *in.Email); err != nil {
			return User{}, err
		} else if taken {
			return User{}, ErrDuplicate
		}
	}
	login, err := r.generateLogin(ctx, in.FirstName, in.LastName)
	if err != nil {
		return User{}, err
	}

	var u User
	err = scanUser(r.pool.QueryRow(ctx, `
		INSERT INTO users AS u (first_name, last_name, national_id, birth_date, gender, email, phone,
			address, postal_code, city, province, emergency_contact, login)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING `+userSelect+`
	`, in.FirstName, in.LastName, in.NationalID, in.BirthDate, in.Gender, in.Email, in.Phone,
		in.Address, in.PostalCode, in.City, in.Province, in.EmergencyContact, login,
	), &u)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return u, ErrDuplicate
	}
	return u, err
}

// maxLoginGenerationAttempts bounds the collision-suffix search below —
// astronomically more than a club this size could ever need, so hitting
// it means something else is wrong rather than a genuinely exhausted
// namespace.
const maxLoginGenerationAttempts = 200

// generateLogin picks the login a brand-new person gets: their name
// slugified and dot-joined (see candidateLogin), or that same value with
// a numeric suffix if someone else already has it — the first
// still-free candidate in that sequence wins.
func (r *Repository) generateLogin(ctx context.Context, firstName, lastName string) (string, error) {
	base := candidateLogin(firstName, lastName)
	for n := 1; n <= maxLoginGenerationAttempts; n++ {
		candidate := base
		if n > 1 {
			candidate = loginWithSuffix(base, n)
		}
		taken, err := r.loginTakenByAnotherUser(ctx, 0, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique login for %q %q after %d attempts", firstName, lastName, maxLoginGenerationAttempts)
}

func (r *Repository) loginTakenByAnotherUser(ctx context.Context, excludeID int64, login string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id <> $1 AND login = $2)
	`, excludeID, login).Scan(&exists)
	return exists, err
}

// FindIDForLogin resolves a login to the numeric id it names. Every
// other package that accepts a login at its own HTTP boundary (a route
// like /users/{login}/tokens, say) needs exactly this translation before
// it can look up, filter, or insert anything keyed by user id — see
// Service.UserIDForLogin, which the rest of the API depends on through a
// small interface defined locally in each of those packages.
func (r *Repository) FindIDForLogin(ctx context.Context, login string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM users WHERE login = $1`, login).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// FindLoginForID is FindIDForLogin's inverse, for the handful of
// responses that need to name a person by an id resolved some other way
// (see authapi.Handler.impersonatorSummary) rather than one a
// repository JOIN already turned into a login inline.
func (r *Repository) FindLoginForID(ctx context.Context, id int64) (string, error) {
	var login string
	err := r.pool.QueryRow(ctx, `SELECT login FROM users WHERE id = $1`, id).Scan(&login)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return login, err
}

func (r *Repository) GetByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userSelect+`
		FROM users u
		WHERE u.id = $1
	`, id), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) GetByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userSelect+`
		FROM users u
		WHERE $1 = ANY(string_to_array(u.email, ' '))
	`, email), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (r *Repository) List(ctx context.Context, limit, offset int) ([]User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userSelect+`
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
		if err := scanUser(rows, &u); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// FindActiveByEmailAndPhone is the login lookup: matches against any one
// of the person's emails and any one of their phones (digits only, so
// formatting differences don't matter), or ErrNotFound for either not
// matching. The caller must not distinguish between those cases in its
// response — see authapi's anti-enumeration design. Anyone in this table
// with both on file can be found this way; what a resulting session can
// actually do is entirely down to roles.
func (r *Repository) FindActiveByEmailAndPhone(ctx context.Context, email, phone string) (User, error) {
	var u User
	err := scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userSelect+`
		FROM users u
		WHERE $1 = ANY(string_to_array(u.email, ' '))
			AND u.phone IS NOT NULL
			AND regexp_replace($2, '\D', '', 'g') <> ''
			AND EXISTS (
				SELECT 1 FROM unnest(string_to_array(u.phone, ' ')) AS candidate
				WHERE regexp_replace(candidate, '\D', '', 'g') = regexp_replace($2, '\D', '', 'g')
			)
	`, email, phone), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// spainCountryCode is the +34 calling code every phone Telegram's
// "share contact" hands back is expected to carry (this is a Spanish
// club — see FindByPhoneNumber) but a phone on file here never does
// (see users.CreateInput and every SEED_ADMIN_PHONE default).
const spainCountryCode = "34"

// FindByPhoneNumber looks up a person by phone the way a Telegram
// "share contact" does it, not the way FindActiveByEmailAndPhone does:
// Telegram always includes the country code (e.g. "34611222333"), while
// a phone on file here never does, so an exact digits match would never
// work as-is. Matching requires digits to equal a candidate exactly, or
// to equal spainCountryCode prepended to one — deliberately not a
// same-length-agnostic suffix match: that would also match, say, a
// French "+33666777888" against a Spanish "666777888" on file (they
// share every digit but the country code), letting anyone claim to be
// whoever's number happens to end the same way. digits must already be
// stripped to only digits by the caller; empty always misses rather
// than matching every phone on file. A phone shared by more than one
// person (each phone field is itself space-separated, so this isn't
// even the only way to be ambiguous) returns whichever one sorts first
// by id — rare enough not to need anything smarter.
func (r *Repository) FindByPhoneNumber(ctx context.Context, digits string) (User, error) {
	var u User
	if digits == "" {
		return u, ErrNotFound
	}
	err := scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userSelect+`
		FROM users u
		WHERE EXISTS (
			SELECT 1 FROM unnest(string_to_array(u.phone, ' ')) AS candidate
			WHERE length(regexp_replace(candidate, '\D', '', 'g')) >= 6
				AND $1 IN (
					regexp_replace(candidate, '\D', '', 'g'),
					$2 || regexp_replace(candidate, '\D', '', 'g')
				)
		)
		ORDER BY u.id
		LIMIT 1
	`, digits, spainCountryCode), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// SetTelegramChatID records which Telegram chat is this person's own,
// once internal/telegrambot has matched a shared contact's phone number
// against them.
func (r *Repository) SetTelegramChatID(ctx context.Context, userID, chatID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET telegram_chat_id = $1 WHERE id = $2`, chatID, userID)
	return err
}

// TelegramChatIDForUser returns the Telegram chat this person has
// linked, or nil if they never have (see SetTelegramChatID) — used to
// also DM them a magic link/login code alongside the email, for
// whoever we can reach that way.
func (r *Repository) TelegramChatIDForUser(ctx context.Context, userID int64) (*int64, error) {
	var chatID *int64
	err := r.pool.QueryRow(ctx, `SELECT telegram_chat_id FROM users WHERE id = $1`, userID).Scan(&chatID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return chatID, err
}

// HasTelegramChatID reports whether some person has already linked this
// Telegram chat — internal/telegrambot only greets a chat it doesn't
// recognize yet.
func (r *Repository) HasTelegramChatID(ctx context.Context, chatID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE telegram_chat_id = $1)`, chatID).Scan(&exists)
	return exists, err
}

func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (User, error) {
	u, err := r.GetByID(ctx, id)
	if err != nil {
		return u, err
	}
	if in.Login != nil {
		login := strings.ToLower(strings.TrimSpace(*in.Login))
		if !loginFormat.MatchString(login) {
			return u, ErrInvalidLogin
		}
		if taken, err := r.loginTakenByAnotherUser(ctx, id, login); err != nil {
			return u, err
		} else if taken {
			return u, ErrDuplicate
		}
		u.Login = login
	}
	if in.FirstName != nil {
		u.FirstName = *in.FirstName
	}
	if in.LastName != nil {
		u.LastName = *in.LastName
	}
	if in.Email.Set {
		u.Email = in.Email.Value
		if in.Email.Value != nil {
			if taken, err := r.emailTakenByAnotherUser(ctx, id, *in.Email.Value); err != nil {
				return u, err
			} else if taken {
				return u, ErrDuplicate
			}
		}
	}
	if in.NationalID.Set {
		u.NationalID = in.NationalID.Value
	}
	if in.BirthDate != nil {
		u.BirthDate = *in.BirthDate
	}
	if in.Gender.Set {
		u.Gender = in.Gender.Value
	}
	if in.Phone.Set {
		u.Phone = in.Phone.Value
	}
	if in.Address.Set {
		u.Address = in.Address.Value
	}
	if in.PostalCode.Set {
		u.PostalCode = in.PostalCode.Value
	}
	if in.City.Set {
		u.City = in.City.Value
	}
	if in.Province.Set {
		u.Province = in.Province.Value
	}
	if in.EmergencyContact.Set {
		u.EmergencyContact = in.EmergencyContact.Value
	}

	_, err = r.pool.Exec(ctx, `
		UPDATE users SET first_name=$1, last_name=$2, email=$3, national_id=$4, birth_date=$5,
			gender=$6, phone=$7, address=$8, postal_code=$9, city=$10, province=$11, emergency_contact=$12,
			login=$13, updated_at=now()
		WHERE id=$14
	`, u.FirstName, u.LastName, u.Email, u.NationalID, u.BirthDate, u.Gender, u.Phone, u.Address,
		u.PostalCode, u.City, u.Province, u.EmergencyContact, u.Login, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return u, ErrDuplicate
	}
	return u, err
}

// EnsureMembershipNumber gives userID the next membership number after
// the current highest one, but only if they don't already have one —
// someone who leaves and later rejoins keeps the number they got the
// first time (see User.MembershipNumber). The new value is computed in
// the same statement that assigns it, keeping the read-then-write race
// window as small as possible; a genuine concurrent collision would
// still be caught by the unique index rather than silently duplicating a
// number.
func (r *Repository) EnsureMembershipNumber(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users
		SET membership_number = (SELECT COALESCE(MAX(membership_number), 0) + 1 FROM users)
		WHERE id = $1 AND membership_number IS NULL
	`, userID)
	return err
}

// Delete removes a person's record entirely, along with their refresh
// tokens, magic-link tokens, membership history and activity sign-ups
// (all ON DELETE CASCADE). It's rejected by the database if the
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

// ListDirectory returns every person on file, for populating pickers
// like an activity's participant list — being a participant, or the
// party to a material transaction, doesn't require being able to sign
// in at all.
func (r *Repository) ListDirectory(ctx context.Context) ([]DirectoryEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.login, u.first_name, u.last_name
		FROM users u
		ORDER BY u.last_name, u.first_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []DirectoryEntry{}
	for rows.Next() {
		var e DirectoryEntry
		if err := rows.Scan(&e.Login, &e.FirstName, &e.LastName); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// TouchLastLogin records when and, if known, from where a session was
// most recently issued. ip is whatever authapi resolved from
// X-Forwarded-For (behind the production reverse proxy) or the
// connection's own address — purely informational, never checked for
// authorization.
func (r *Repository) TouchLastLogin(ctx context.Context, userID int64, ip string) error {
	var ipArg any
	if ip != "" {
		ipArg = ip
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET last_login_at = now(), last_login_ip = $2 WHERE id = $1
	`, userID, ipArg)
	return err
}

// maxMagicLinkCodeAttempts bounds brute-forcing a magic link's 6-digit
// login code: once a still-live row has racked up this many wrong
// guesses, it stops accepting the code at all (the link itself is
// unaffected — see ConsumeMagicLinkCode).
const maxMagicLinkCodeAttempts = 8

// CreateMagicLinkToken stores the hash of a freshly issued sign-in link,
// together with the hash of the numeric code sent in the same email.
func (r *Repository) CreateMagicLinkToken(ctx context.Context, userID int64, tokenHash, codeHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO magic_link_tokens (user_id, token_hash, code_hash, expires_at) VALUES ($1,$2,$3,$4)
	`, userID, tokenHash, codeHash, expiresAt)
	return err
}

// ConsumeMagicLinkToken atomically marks a token used and returns whose it
// was, or ErrInvalidToken if it doesn't exist, already got used, or
// expired — so a token can never be replayed. Unaffected by how many
// times its companion code has been guessed wrong (see
// ConsumeMagicLinkCode): the link itself never locks out.
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

// ConsumeMagicLinkCode consumes userID's still-live magic-link code if
// codeHash matches, the same one-shot way ConsumeMagicLinkToken consumes
// the link. userID is expected to already be resolved (by email+phone,
// exactly like requesting a link), not looked up from the code itself —
// the code alone is too low-entropy to serve as a lookup key. A wrong
// guess counts against every currently-live row for this user (not just
// one), so repeated guessing against a pending request is bounded by
// maxMagicLinkCodeAttempts regardless of which row it targets.
func (r *Repository) ConsumeMagicLinkCode(ctx context.Context, userID int64, codeHash string) (int64, error) {
	var gotUserID int64
	err := r.pool.QueryRow(ctx, `
		UPDATE magic_link_tokens SET consumed_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND consumed_at IS NULL
			AND expires_at > now() AND code_attempts < $3
		RETURNING user_id
	`, userID, codeHash, maxMagicLinkCodeAttempts).Scan(&gotUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, updateErr := r.pool.Exec(ctx, `
			UPDATE magic_link_tokens SET code_attempts = code_attempts + 1
			WHERE user_id = $1 AND consumed_at IS NULL AND expires_at > now()
		`, userID); updateErr != nil {
			return 0, updateErr
		}
		return 0, ErrInvalidToken
	}
	return gotUserID, err
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

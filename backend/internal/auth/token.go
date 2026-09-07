package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

var ErrInvalidToken = errors.New("invalid, expired, or revoked token")

// SessionResolver turns a raw bearer token into the user it belongs to.
// The tokens package is the real implementation, backed by the database
// (a token is opaque and revocable, so there's no way to check it without
// one); this interface exists so this package doesn't need to depend on
// that one.
type SessionResolver interface {
	Authenticate(ctx context.Context, rawToken, ip string) (AuthenticatedUser, error)
}

type Authenticator struct {
	resolver SessionResolver
}

func NewAuthenticator(resolver SessionResolver) *Authenticator {
	return &Authenticator{resolver: resolver}
}

// NewOpaqueToken returns a random, high-entropy (256-bit) token. The
// caller is responsible for persisting its hash and expiry — used for
// session tokens (30 days, config.Config.TokenTTL) and impersonation
// tokens (see tokens.Service), both long-lived bearer credentials that
// authenticate every request for as long as they're valid, and never
// shown in a URL — see NewMagicLinkToken for the one opaque token in
// this app that's the other way around.
func NewOpaqueToken() (string, error) {
	return randomToken(32)
}

// NewMagicLinkToken returns a random token sized for a magic-link URL —
// deliberately smaller than NewOpaqueToken's 256 bits. Unlike a session
// token, this one only ever needs to resist guessing within
// MagicLinkTTL's own short window (15 minutes by default), and it's the
// one opaque token in this app that actually ends up in a URL, so its
// length matters in a way a header-only bearer token's doesn't. 128
// bits (16 bytes, ~22 base64 characters instead of ~43) is still the
// widely-cited baseline for this kind of secret — OWASP's own
// recommended *minimum* for a session identifier — and astronomically
// out of reach for brute-forcing in 15 minutes: even an unrealistic
// 100,000 guesses/second sustained for the whole window covers a
// negligible fraction of 2^128 possibilities. That margin matters here
// specifically because nothing else rate-limits guesses against
// POST /auth/magic-link/consume the way the low-entropy login code is
// (see users.Repository.ConsumeMagicLinkCode) — token entropy is the
// only defense this endpoint has.
func NewMagicLinkToken() (string, error) {
	return randomToken(16)
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken digests an opaque token so only the hash, never the token
// itself, needs to be stored — a leaked database row can't be replayed
// as a session or a sign-in link by itself.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewLoginCode returns a random 6-digit numeric string, zero-padded
// (e.g. "042817") — the low-entropy companion to a magic link's URL,
// meant to be read from the same email and typed into the login page by
// hand. Useful when the email is only reachable from a different device
// than the one signing in. Its hash is stored and checked the same way
// as an opaque token's (see HashToken); low entropy is compensated for
// by expiry and a per-request attempt limit (see
// users.Repository.ConsumeMagicLinkCode), not by making it hard to type.
func NewLoginCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

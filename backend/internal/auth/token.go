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

// NewOpaqueToken returns a random, high-entropy token. The caller is
// responsible for persisting its hash and expiry — used for both session
// tokens and magic-link tokens.
func NewOpaqueToken() (string, error) {
	buf := make([]byte, 32)
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

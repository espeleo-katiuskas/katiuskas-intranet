package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

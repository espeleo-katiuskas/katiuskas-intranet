package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid or expired token")

// Claims are intentionally minimal: enough for the API to authorize a
// request without a database round trip. The refresh token, not this
// struct, is the source of truth for session validity.
type Claims struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

type Authenticator struct {
	secret         []byte
	accessTokenTTL time.Duration
}

func NewAuthenticator(secret string, accessTokenTTL time.Duration) *Authenticator {
	return &Authenticator{secret: []byte(secret), accessTokenTTL: accessTokenTTL}
}

func (a *Authenticator) IssueAccessToken(userID int64, role string) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(a.accessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secret)
}

func (a *Authenticator) ParseAccessToken(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		return a.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// NewRefreshToken returns an opaque, high-entropy token. The caller is
// responsible for persisting its hash and expiry.
func NewRefreshToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

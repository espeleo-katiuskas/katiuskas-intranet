package auth

import (
	"errors"
	"testing"
	"time"
)

func TestIssueAndParseAccessToken(t *testing.T) {
	a := NewAuthenticator("test-secret", time.Minute)

	token, err := a.IssueAccessToken(42, []string{"admin", "board"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	claims, err := a.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("expected user id 42, got %d", claims.UserID)
	}
	if len(claims.Roles) != 2 || claims.Roles[0] != "admin" || claims.Roles[1] != "board" {
		t.Errorf("expected roles [admin board], got %v", claims.Roles)
	}
}

func TestParseAccessTokenRejectsExpired(t *testing.T) {
	a := NewAuthenticator("test-secret", -time.Minute)
	token, err := a.IssueAccessToken(1, []string{"member"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, err := a.ParseAccessToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestParseAccessTokenRejectsWrongSecret(t *testing.T) {
	a1 := NewAuthenticator("secret-one", time.Minute)
	a2 := NewAuthenticator("secret-two", time.Minute)

	token, err := a1.IssueAccessToken(1, []string{"member"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, err := a2.ParseAccessToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for wrong secret, got %v", err)
	}
}

func TestParseAccessTokenRejectsGarbage(t *testing.T) {
	a := NewAuthenticator("secret", time.Minute)
	if _, err := a.ParseAccessToken("not-a-jwt"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestNewRefreshTokenIsUniqueAndNonEmpty(t *testing.T) {
	a, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("new refresh token: %v", err)
	}
	b, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("new refresh token: %v", err)
	}
	if a == "" || b == "" {
		t.Fatalf("expected non-empty tokens")
	}
	if a == b {
		t.Fatalf("expected two calls to produce different tokens, got the same one twice")
	}
}

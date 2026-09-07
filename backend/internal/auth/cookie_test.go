package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetSessionCookie(t *testing.T) {
	t.Run("https public base URL sets Secure", func(t *testing.T) {
		rec := httptest.NewRecorder()
		SetSessionCookie(rec, "raw-token", "https://intranet.example.test", time.Hour)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected exactly one cookie, got %d", len(cookies))
		}
		c := cookies[0]
		if c.Name != SessionCookieName || c.Value != "raw-token" {
			t.Errorf("unexpected cookie name/value: %+v", c)
		}
		if !c.Secure {
			t.Errorf("expected Secure over an https PublicBaseURL")
		}
		if !c.HttpOnly {
			t.Errorf("expected HttpOnly")
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("expected SameSite=Lax, got %v", c.SameSite)
		}
		if c.Path != cookiePath {
			t.Errorf("expected Path %q, got %q", cookiePath, c.Path)
		}
	})

	t.Run("plain http public base URL leaves Secure off", func(t *testing.T) {
		rec := httptest.NewRecorder()
		SetSessionCookie(rec, "raw-token", "http://localhost:64710", time.Hour)

		c := rec.Result().Cookies()[0]
		if c.Secure {
			t.Errorf("expected no Secure over a plain http PublicBaseURL (dev), a real browser would drop the cookie entirely")
		}
	})
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, "https://intranet.example.test")

	c := rec.Result().Cookies()[0]
	if c.Name != SessionCookieName {
		t.Errorf("unexpected cookie name: %q", c.Name)
	}
	if c.Value != "" {
		t.Errorf("expected an empty value, got %q", c.Value)
	}
	if c.MaxAge >= 0 {
		t.Errorf("expected a negative MaxAge to delete the cookie, got %d", c.MaxAge)
	}
}

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIPRateLimiterAllowsUpToLimitThenBlocks(t *testing.T) {
	l := NewIPRateLimiter(2, time.Minute)

	if !l.Allow("1.2.3.4") {
		t.Fatal("expected 1st call to be allowed")
	}
	if !l.Allow("1.2.3.4") {
		t.Fatal("expected 2nd call to be allowed")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("expected 3rd call within the same window to be blocked")
	}
}

func TestIPRateLimiterTracksKeysIndependently(t *testing.T) {
	l := NewIPRateLimiter(1, time.Minute)

	if !l.Allow("1.2.3.4") {
		t.Fatal("expected 1st call from 1.2.3.4 to be allowed")
	}
	if !l.Allow("5.6.7.8") {
		t.Fatal("expected a different key to have its own independent limit")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("expected 1.2.3.4's own 2nd call to still be blocked")
	}
}

func TestIPRateLimiterResetsAfterWindow(t *testing.T) {
	l := NewIPRateLimiter(1, time.Millisecond)

	if !l.Allow("1.2.3.4") {
		t.Fatal("expected 1st call to be allowed")
	}
	time.Sleep(5 * time.Millisecond)
	if !l.Allow("1.2.3.4") {
		t.Fatal("expected the call after the window elapsed to be allowed again")
	}
}

func TestIPRateLimiterZeroLimitDisablesLimiting(t *testing.T) {
	l := NewIPRateLimiter(0, time.Minute)

	for i := 0; i < 10; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("call %d: expected a zero limit to always allow", i)
		}
	}
}

func TestIPRateLimiterLimitMiddlewareAnswers429OnceExceeded(t *testing.T) {
	l := NewIPRateLimiter(1, time.Minute)
	handler := l.Limit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/whatever", nil)
	req.RemoteAddr = "9.9.9.9:1234"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 1st request to pass through with 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 2nd request from the same address to get 429, got %d", rec.Code)
	}
}

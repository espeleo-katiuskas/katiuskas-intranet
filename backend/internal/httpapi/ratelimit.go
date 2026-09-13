package httpapi

import (
	"net/http"
	"sync"
	"time"
)

// IPRateLimiter throttles repeated calls to one endpoint from the same
// client address using a plain in-memory fixed-window counter — no new
// dependency, matching this project's already-lean go.mod.
//
// Keyed by ClientIP(r), whose own doc comment calls it "purely
// informational... never used for any access decision" — this is the
// one deliberate exception. Unlike an auth/role check, a rate limit is
// inherently best-effort rather than an authoritative guarantee, so
// treating ClientIP as a reasonable-but-spoofable hint here doesn't
// violate that caveat, it's consistent with it: whatever sits in front
// of this service (see docker-compose.yml/deploy/Caddyfile, and the
// host's own reverse proxy in production) controls how trustworthy
// X-Forwarded-For actually is, so a caller determined enough can still
// send a different one on every request and dodge this entirely. That
// raises the cost of casual abuse (a script hammering this endpoint
// from one address with random email/phone guesses) — it doesn't
// stop a determined attacker the way the per-user magic-link cooldown
// or the per-code attempt counter do, since those are keyed by an
// identity resolved from the database rather than a client-controlled
// header.
type IPRateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	windows map[string]*ipWindow
	calls   uint64
}

type ipWindow struct {
	count   int
	resetAt time.Time
}

// sweepEvery bounds how often Allow does a full pass over windows to
// drop expired entries, so memory stays proportional to recently-active
// keys rather than growing forever — a call count rather than a ticker
// goroutine, since this only needs to run under the same lock Allow
// already takes, not on its own schedule.
const sweepEvery = 500

// NewIPRateLimiter builds a limiter allowing up to limit calls per
// window for each distinct key. limit <= 0 disables rate limiting
// outright (Allow and Limit always let the request through) — the zero
// value an authapi.Options left unset (as every existing test's
// newTestServer helper does) falls into this, rather than blocking
// every request the way a literal 0-per-window limit otherwise would.
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	return &IPRateLimiter{limit: limit, window: window, windows: make(map[string]*ipWindow)}
}

// Allow reports whether one more call under key is still within limit
// for the current window, counting this call if so.
func (l *IPRateLimiter) Allow(key string) bool {
	if l.limit <= 0 {
		return true
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%sweepEvery == 0 {
		for k, w := range l.windows {
			if now.After(w.resetAt) {
				delete(l.windows, k)
			}
		}
	}

	w, ok := l.windows[key]
	if !ok || now.After(w.resetAt) {
		l.windows[key] = &ipWindow{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// Limit wraps next, answering 429 instead of calling it once the caller
// (identified by ClientIP) has exceeded this limiter's own limit/window.
func (l *IPRateLimiter) Limit(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests from this address, try again later")
			return
		}
		next(w, r)
	})
}

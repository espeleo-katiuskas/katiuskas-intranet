package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP resolves the caller's address: the first entry of
// X-Forwarded-For when present (the production deploy sits behind a
// reverse proxy that sets it), otherwise the connection's own remote
// address. This is purely informational — never used for any access
// decision — since a client-supplied header is only as trustworthy as
// whatever's in front of this service actually setting it, rather than
// passing through whatever the client sent. IPRateLimiter (see
// ratelimit.go) is the one deliberate exception: a rate limit is
// inherently best-effort rather than authoritative, so using this as a
// spoofable-but-still-useful hint there doesn't violate this comment's
// own rule, it's the case that rule was never meant to cover.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

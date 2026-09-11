package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP resolves the caller's address: the last entry of
// X-Forwarded-For when present, otherwise the connection's own remote
// address. The *last* entry, deliberately, not the first: Caddy's
// reverse_proxy (both deploy/Caddyfile in front of this API, and
// whatever host-level proxy sits in front of that in production, see
// docs/deployment.md) appends the peer it actually saw to any
// X-Forwarded-For a request already carried, rather than replacing it
// — so the first entry is whatever the client itself sent, no more
// trustworthy than any other client-controlled header, while the last
// one is this API's own immediate proxy hop reporting what it saw with
// its own eyes. This is purely informational — never used for any
// access decision — since even the last entry is only as trustworthy
// as that immediate hop actually is. IPRateLimiter (see ratelimit.go)
// is the one deliberate exception: a rate limit is inherently
// best-effort rather than authoritative, so using this as a
// spoofable-but-still-useful hint there doesn't violate this comment's
// own rule, it's the case that rule was never meant to cover.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		last := fwd
		if i := strings.LastIndexByte(fwd, ','); i != -1 {
			last = fwd[i+1:]
		}
		return strings.TrimSpace(last)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

package auth

import (
	"net/http"
	"strings"
	"time"
)

// SessionCookieName carries the exact same opaque session token as the
// Authorization header — set at sign-in (and impersonation start/exit)
// time, read only by RequireAuthFromHeaderOrCookie/ProtectAllowingCookie
// for the specific read-only file-serving routes that need one (see
// there for why). Every other route only ever looks at the header, so
// this cookie existing changes nothing about them.
const SessionCookieName = "katiuskas_session"

// cookiePath scopes the cookie to the API itself — the frontend's own
// static pages never need it, so there's no reason for the browser to
// attach it to those requests too.
const cookiePath = "/api/v1"

// SetSessionCookie sets SessionCookieName to token, valid for maxAge.
// Secure is derived from publicBaseURL (config.Config.PublicBaseURL)
// rather than a separate flag: a plain http:// dev stack couldn't set a
// Secure cookie at all (browsers silently refuse to store one outside
// a TLS connection), while every real deployment's own PublicBaseURL is
// https:// (see docs/deployment.md) — the one signal this app already
// has for "are we actually running behind TLS".
func SetSessionCookie(w http.ResponseWriter, token, publicBaseURL string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     cookiePath,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(publicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes SessionCookieName — called on logout, so a
// signed-out browser can no longer open a file link straight from
// history or a bookmark.
func ClearSessionCookie(w http.ResponseWriter, publicBaseURL string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(publicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

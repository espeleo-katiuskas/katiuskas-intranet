package auth

import (
	"context"
	"net/http"
	"strings"

	"katiuskas-intranet/backend/internal/httpapi"
)

type contextKey string

const userContextKey contextKey = "auth.user"

type AuthenticatedUser struct {
	ID    int64
	Roles []string
	// ImpersonatedBy is set only when this session was minted by
	// POST /users/{userID}/impersonate: the id of the admin acting as
	// ID, not ID itself — everything else (permissions, ID, Roles) is
	// resolved exactly as if ID had signed in normally, so this is the
	// only place that knows an impersonation is happening at all.
	ImpersonatedBy *int64
}

// HasAnyRole reports whether the user holds at least one of the given
// role names.
func (u AuthenticatedUser) HasAnyRole(names ...string) bool {
	for _, want := range names {
		for _, have := range u.Roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		user, err := a.resolver.Authenticate(r.Context(), token, httpapi.ClientIP(r))
		if err != nil {
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid, expired, or revoked token")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole rejects the request unless the authenticated user holds at
// least one of the given roles. It must run after RequireAuth.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok || !user.HasAnyRole(roles...) {
				httpapi.WriteError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserFromContext(ctx context.Context) (AuthenticatedUser, bool) {
	user, ok := ctx.Value(userContextKey).(AuthenticatedUser)
	return user, ok
}

// BaselineRoles are the only roles allowed to use the API at all — a
// club policy, not just a default: it applies on top of whatever more
// specific roles a route also requires, so someone holding only, say,
// "material" still can't call the material endpoints unless they're
// also admin, board, or a current member. Anyone whose membership has
// lapsed (see the roles package: "member" is computed from active
// membership) loses API access the moment it does, even if their
// account and any other role names are still on file.
var BaselineRoles = []string{"admin", "board", "member"}

// HasBaselineRole reports whether the given role names include at least
// one of BaselineRoles — the same test Protect applies to every request,
// exposed standalone for callers (like authapi's magic-link flow) that
// need to check it before there's a request to protect.
func HasBaselineRole(roleNames []string) bool {
	return AuthenticatedUser{Roles: roleNames}.HasAnyRole(BaselineRoles...)
}

// Protect wraps a handler with authentication and authorization: every
// route requires at least one of BaselineRoles, and, when roles are
// given too, also at least one of those. It's the single entry point
// resource handlers use to register a route, so RequireAuth and
// RequireRole are always composed in the right order (auth first, so
// RequireRole finds a user in context).
func (a *Authenticator) Protect(next http.HandlerFunc, roles ...string) http.Handler {
	var handler http.Handler = next
	if len(roles) > 0 {
		handler = RequireRole(roles...)(handler)
	}
	handler = RequireRole(BaselineRoles...)(handler)
	return a.RequireAuth(handler)
}

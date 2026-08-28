package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const userContextKey contextKey = "auth.user"

type AuthenticatedUser struct {
	ID   int64
	Role string
}

func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := a.ParseAccessToken(token)
		if err != nil {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, AuthenticatedUser{
			ID:   claims.UserID,
			Role: claims.Role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole rejects the request unless the authenticated user holds one
// of the given roles. It must run after RequireAuth.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, role := range roles {
		allowed[role] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok || !allowed[user.Role] {
				http.Error(w, "insufficient permissions", http.StatusForbidden)
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

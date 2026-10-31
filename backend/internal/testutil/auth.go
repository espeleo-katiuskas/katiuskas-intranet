package testutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
)

// FakeResolver is an in-memory auth.SessionResolver, so handler tests
// across every package can mint bearer tokens for arbitrary users and
// roles without going through the real, database-backed tokens package
// (which has its own tests). Not safe for concurrent use across
// goroutines, same as the rest of this package's helpers.
type FakeResolver map[string]auth.AuthenticatedUser

func (f FakeResolver) Authenticate(_ context.Context, rawToken, _, _ string) (auth.AuthenticatedUser, error) {
	user, ok := f[rawToken]
	if !ok {
		return auth.AuthenticatedUser{}, auth.ErrInvalidToken
	}
	return user, nil
}

// NewTestAuthenticator returns an authenticator backed by a FakeResolver,
// for handler tests that only care about role-based authorization, not
// real session/token mechanics (those are covered by the auth and tokens
// packages' own tests).
func NewTestAuthenticator() (*auth.Authenticator, FakeResolver) {
	resolver := FakeResolver{}
	return auth.NewAuthenticator(resolver), resolver
}

// TokenFor registers and returns a fake bearer token for the given user
// id and roles.
func TokenFor(resolver FakeResolver, id int64, roleNames ...string) string {
	token := fmt.Sprintf("test-token-%d-%s", id, strings.Join(roleNames, ","))
	resolver[token] = auth.AuthenticatedUser{ID: id, Roles: roleNames}
	return token
}

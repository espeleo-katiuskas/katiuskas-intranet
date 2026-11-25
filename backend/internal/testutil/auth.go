package testutil

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
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
	subject, login := identityOf(id)
	resolver[token] = auth.AuthenticatedUser{ID: id, Subject: subject, Login: login, Roles: roleNames}
	return token
}

// identityOf is the subject and the login of the person a test inserted with that id. For an id no helper
// inserted (a test that makes up a session for somebody who is not in the database) they are made from
// the id: valid, and naming nobody.
func identityOf(id int64) (subject, login string) {
	if p, ok := people.Load(id); ok {
		return p.(personIdentity).subject, p.(personIdentity).login
	}
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", id), fmt.Sprintf("test-user-%d", id)
}

// CatalogueAccount is the account of the catalogue of places of a person, made if they have none: what the
// catalogue keeps of who made something, for a test that puts rows in the catalogue by hand.
func CatalogueAccount(t *testing.T, tx pgx.Tx, user TestUser) int64 {
	t.Helper()
	account, err := accounts.NewService(accounts.NewRepository(tx)).Ensure(context.Background(), access.Viewer{
		Issuer: access.IntranetIssuer, Subject: user.Subject, Name: user.Login,
	})
	if err != nil {
		t.Fatalf("the catalogue account of %s: %v", user.Login, err)
	}
	return account.ID
}

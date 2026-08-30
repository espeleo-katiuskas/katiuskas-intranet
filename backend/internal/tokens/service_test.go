package tokens_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/tokens"
)

func TestIssueThenAuthenticate(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	svc := tokens.NewService(tokens.NewRepository(tx), rolesService)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := rolesService.AssignByName(ctx, user.ID, []string{"board"}); err != nil {
		t.Fatalf("assign role: %v", err)
	}

	raw, err := svc.Issue(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if raw == "" {
		t.Fatalf("expected a non-empty raw token")
	}

	got, err := svc.Authenticate(ctx, raw, "198.51.100.7")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, got.ID)
	}
	if !got.HasAnyRole("board") {
		t.Errorf("expected roles to include board, got %v", got.Roles)
	}
}

func TestAuthenticateRejectsUnknownToken(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx)))

	if _, err := svc.Authenticate(context.Background(), "never-issued", ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected auth.ErrInvalidToken, got %v", err)
	}
}

func TestRevokeRejectsTokenBelongingToAnotherUser(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx)))
	ctx := context.Background()

	owner := testutil.InsertUser(t, tx, "")
	someoneElse := testutil.InsertUser(t, tx, "")
	raw, err := svc.Issue(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	repo := tokens.NewRepository(tx)
	list, err := repo.ListForUser(ctx, owner.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected exactly 1 token for owner, got %d (err %v)", len(list), err)
	}

	if err := svc.Revoke(ctx, someoneElse.ID, list[0].ID); !errors.Is(err, tokens.ErrWrongUser) {
		t.Errorf("expected ErrWrongUser, got %v", err)
	}

	// The token must still work — revoking under the wrong user id must
	// not have silently succeeded.
	if _, err := svc.Authenticate(ctx, raw, ""); err != nil {
		t.Errorf("expected the token to still authenticate, got %v", err)
	}
}

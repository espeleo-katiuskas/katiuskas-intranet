package tokens_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
)

func TestIssueThenAuthenticate(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	svc := tokens.NewService(tokens.NewRepository(tx), rolesService)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	// "board" is automatic now — computed from board_members, not
	// grantable through AssignByName — so give them an open board term
	// directly instead of assigning the role by hand.
	if _, err := tx.Exec(ctx, `
		INSERT INTO board_members (user_id, position, start_date) VALUES ($1, 'secretary', '2020-01-01')
	`, user.ID); err != nil {
		t.Fatalf("insert board membership: %v", err)
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

func TestImpersonateRejectsSelf(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx)))
	ctx := context.Background()

	admin := testutil.InsertUser(t, tx, "")
	if _, err := svc.Impersonate(ctx, admin.ID, admin.ID, time.Hour); !errors.Is(err, tokens.ErrCannotImpersonateSelf) {
		t.Errorf("expected ErrCannotImpersonateSelf, got %v", err)
	}
}

func TestImpersonateRejectsIneligibleTarget(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx)))
	ctx := context.Background()

	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "") // no roles at all — not baseline-eligible

	if _, err := svc.Impersonate(ctx, admin.ID, target.ID, time.Hour); !errors.Is(err, tokens.ErrNotEligibleForImpersonation) {
		t.Errorf("expected ErrNotEligibleForImpersonation, got %v", err)
	}
}

func TestImpersonateRejectsUnknownTarget(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx)))
	admin := testutil.InsertUser(t, tx, "")

	if _, err := svc.Impersonate(context.Background(), admin.ID, 999999, time.Hour); !errors.Is(err, tokens.ErrNotEligibleForImpersonation) {
		t.Errorf("expected ErrNotEligibleForImpersonation for an unknown target, got %v", err)
	}
}

func TestImpersonateSucceedsAndResolvesTargetsRoles(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	svc := tokens.NewService(tokens.NewRepository(tx), rolesService)
	ctx := context.Background()

	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if err := rolesService.AssignByName(ctx, target.ID, []string{"admin"}); err != nil {
		t.Fatalf("assign admin to target: %v", err)
	}

	raw, err := svc.Impersonate(ctx, admin.ID, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("impersonate: %v", err)
	}

	resolved, err := svc.Authenticate(ctx, raw, "")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if resolved.ID != target.ID {
		t.Errorf("expected the session to resolve to the target %d, got %d", target.ID, resolved.ID)
	}
	if !resolved.HasAnyRole("admin") {
		t.Errorf("expected the session to carry the target's roles, got %v", resolved.Roles)
	}
	if resolved.ImpersonatedBy == nil || *resolved.ImpersonatedBy != admin.ID {
		t.Errorf("expected ImpersonatedBy %d, got %v", admin.ID, resolved.ImpersonatedBy)
	}
}

func TestExitImpersonationRevokesAndIssuesFreshAdminToken(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	svc := tokens.NewService(tokens.NewRepository(tx), rolesService)
	ctx := context.Background()

	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if err := rolesService.AssignByName(ctx, target.ID, []string{"admin"}); err != nil {
		t.Fatalf("assign admin to target: %v", err)
	}
	impersonationToken, err := svc.Impersonate(ctx, admin.ID, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("impersonate: %v", err)
	}

	newAdminToken, err := svc.ExitImpersonation(ctx, impersonationToken, admin.ID, time.Hour)
	if err != nil {
		t.Fatalf("exit impersonation: %v", err)
	}
	if newAdminToken == impersonationToken {
		t.Error("expected a brand-new token, not the same impersonation one")
	}

	if _, err := svc.Authenticate(ctx, impersonationToken, ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected the impersonation token to be revoked, got %v", err)
	}

	resolved, err := svc.Authenticate(ctx, newAdminToken, "")
	if err != nil {
		t.Fatalf("authenticate with the new token: %v", err)
	}
	if resolved.ID != admin.ID {
		t.Errorf("expected the new token to resolve to the admin %d, got %d", admin.ID, resolved.ID)
	}
	if resolved.ImpersonatedBy != nil {
		t.Errorf("expected the new token to be a normal session, got impersonated_by %v", resolved.ImpersonatedBy)
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

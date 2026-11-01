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

	got, err := svc.Authenticate(ctx, raw, "198.51.100.7", "")
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

	if _, err := svc.Authenticate(context.Background(), "never-issued", "", ""); !errors.Is(err, auth.ErrInvalidToken) {
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

	resolved, err := svc.Authenticate(ctx, raw, "", "")
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

	if _, err := svc.Authenticate(ctx, impersonationToken, "", ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expected the impersonation token to be revoked, got %v", err)
	}

	resolved, err := svc.Authenticate(ctx, newAdminToken, "", "")
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
	if _, err := svc.Authenticate(ctx, raw, "", ""); err != nil {
		t.Errorf("expected the token to still authenticate, got %v", err)
	}
}

// A session expires a while after its LAST use, not after it was created: each use moves the
// expiry to now plus the token's lifetime.
func TestUseRenewsASessionButNotAFixedOne(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	svc := tokens.NewService(repo, roles.NewService(roles.NewRepository(tx)))
	ctx := context.Background()
	user := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")

	expiry := func(t *testing.T, hash string) time.Duration {
		t.Helper()
		var d time.Duration
		var secs float64
		if err := tx.QueryRow(ctx, `SELECT extract(epoch FROM expires_at - now()) FROM tokens WHERE token_hash = $1`, hash).Scan(&secs); err != nil {
			t.Fatalf("read the expiry: %v", err)
		}
		d = time.Duration(secs * float64(time.Second))
		return d
	}
	near := func(got, want time.Duration) bool { return got > want-time.Minute && got < want+time.Minute }
	// Inside one transaction now() does not advance, so "time passes" by pulling the expiry in.
	pullIn := func(hash string) {
		t.Helper()
		if _, err := tx.Exec(ctx, `UPDATE tokens SET expires_at = now() + interval '10 minutes' WHERE token_hash = $1`, hash); err != nil {
			t.Fatal(err)
		}
	}

	// An ordinary session, 30 days.
	raw, err := svc.Issue(ctx, user.ID, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash := auth.HashToken(raw)
	if got := expiry(t, hash); !near(got, 30*24*time.Hour) {
		t.Errorf("expected 30 days from creation, got %v", got)
	}
	pullIn(hash)
	if _, err := svc.Authenticate(ctx, raw, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := expiry(t, hash); !near(got, 30*24*time.Hour) {
		t.Errorf("a use should renew it to 30 days from now, got %v", got)
	}

	// A short one (3 hours) renews to 3 hours, not to 30 days.
	short, _ := svc.Issue(ctx, user.ID, 3*time.Hour)
	shortHash := auth.HashToken(short)
	pullIn(shortHash)
	if _, err := svc.Authenticate(ctx, short, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := expiry(t, shortHash); !near(got, 3*time.Hour) {
		t.Errorf("expected 3 hours from the use, got %v", got)
	}

	// A fixed one (an impersonation) is not moved by use.
	imp, err := repo.CreateImpersonation(ctx, user.ID, admin.ID, "imp-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	pullIn("imp-hash")
	if _, _, err := repo.Authenticate(ctx, "imp-hash", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := expiry(t, "imp-hash"); !near(got, 10*time.Minute) {
		t.Errorf("a fixed expiry should not move with use, got %v (token %d)", got, imp.ID)
	}

	// Neither expired nor revoked sessions can be brought back by use.
	if _, err := tx.Exec(ctx, `UPDATE tokens SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, raw, "", ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("an expired session must stay expired, got %v", err)
	}
	if err := svc.RevokeByHash(ctx, short); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, short, "", ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a revoked session must stay revoked, got %v", err)
	}

	// No session without a positive lifetime.
	if _, err := svc.Issue(ctx, user.ID, 0); !errors.Is(err, tokens.ErrInvalidLifetime) {
		t.Errorf("expected ErrInvalidLifetime, got %v", err)
	}
}

// An admin who sets when a session ends sets how long it lasts from now on, so the next use does not
// undo the change: shortening a session in use, and extending one, both stick.
func TestSettingTheExpiryChangesTheLifetimeToo(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	svc := tokens.NewService(repo, roles.NewService(roles.NewRepository(tx)))
	ctx := context.Background()
	user := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	left := func(hash string) time.Duration {
		var secs float64
		if err := tx.QueryRow(ctx, `SELECT extract(epoch FROM expires_at - now()) FROM tokens WHERE token_hash = $1`, hash).Scan(&secs); err != nil {
			t.Fatal(err)
		}
		return time.Duration(secs * float64(time.Second))
	}
	near := func(got, want time.Duration) bool { return got > want-time.Minute && got < want+time.Minute }
	idOf := func(hash string) int64 {
		id, err := repo.IDByHash(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	raw, _ := svc.Issue(ctx, user.ID, 30*24*time.Hour)
	hash := auth.HashToken(raw)
	if err := svc.UpdateExpiry(ctx, user.ID, idOf(hash), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, raw, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := left(hash); !near(got, 2*time.Hour) {
		t.Errorf("a session shortened to 2 hours should renew to 2 hours, not back to 30 days: %v", got)
	}
	if err := svc.UpdateExpiry(ctx, user.ID, idOf(hash), time.Now().Add(90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, raw, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := left(hash); !near(got, 90*24*time.Hour) {
		t.Errorf("a session extended to 90 days should renew to 90 days: %v", got)
	}
	// A moment already past ends it and leaves the lifetime as it was.
	if err := svc.UpdateExpiry(ctx, user.ID, idOf(hash), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, raw, "", ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a session ended by an admin must be over, got %v", err)
	}

	// A fixed one stays fixed.
	if _, err := repo.CreateImpersonation(ctx, user.ID, admin.ID, "imp", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateExpiry(ctx, user.ID, idOf("imp"), time.Now().Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Authenticate(ctx, "imp", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := left("imp"); !near(got, 5*time.Hour) {
		t.Errorf("an impersonation session keeps the expiry it was given, got %v", got)
	}
}

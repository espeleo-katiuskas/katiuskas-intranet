package tokens_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
)

func TestCreateAndAuthenticateRoundTrip(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	created, err := repo.Create(ctx, user.ID, "hash-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("expected a non-zero id")
	}
	if created.LastUsedAt != nil || created.LastUsedIP != nil {
		t.Errorf("expected a freshly created token to have no last-use info, got %+v", created)
	}

	gotID, impersonatedBy, err := repo.Authenticate(ctx, "hash-1", "203.0.113.5", "")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, gotID)
	}
	if impersonatedBy != nil {
		t.Errorf("expected a normal token to have no impersonated_by, got %v", *impersonatedBy)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.LastUsedAt == nil {
		t.Errorf("expected Authenticate to have set last_used_at")
	}
	if got.LastUsedIP == nil || *got.LastUsedIP != "203.0.113.5" {
		t.Errorf("expected last_used_ip to be 203.0.113.5, got %v", got.LastUsedIP)
	}
}

func TestCreateImpersonationRoundTrip(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")

	created, err := repo.CreateImpersonation(ctx, target.ID, admin.ID, "impersonation-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create impersonation: %v", err)
	}
	if created.UserID != target.ID {
		t.Errorf("expected user_id %d (the target), got %d", target.ID, created.UserID)
	}
	if created.ImpersonatedBy == nil || *created.ImpersonatedBy != admin.ID {
		t.Errorf("expected impersonated_by %d, got %v", admin.ID, created.ImpersonatedBy)
	}

	gotID, impersonatedBy, err := repo.Authenticate(ctx, "impersonation-hash", "", "")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotID != target.ID {
		t.Errorf("expected authenticate to resolve to the target %d, got %d", target.ID, gotID)
	}
	if impersonatedBy == nil || *impersonatedBy != admin.ID {
		t.Errorf("expected impersonated_by %d from Authenticate, got %v", admin.ID, impersonatedBy)
	}
}

func TestAuthenticateRejectsExpired(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, user.ID, "expired-hash", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := repo.Authenticate(ctx, "expired-hash", "", ""); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an expired token, got %v", err)
	}
}

func TestAuthenticateRejectsRevoked(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	created, err := repo.Create(ctx, user.ID, "revoked-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Revoke(ctx, created.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := repo.Authenticate(ctx, "revoked-hash", "", ""); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a revoked token, got %v", err)
	}
}

func TestAuthenticateRejectsUnknownHash(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)

	if _, _, err := repo.Authenticate(context.Background(), "never-issued", "", ""); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRevokeByHash(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, user.ID, "logout-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.RevokeByHash(ctx, "logout-hash"); err != nil {
		t.Fatalf("revoke by hash: %v", err)
	}
	if _, _, err := repo.Authenticate(ctx, "logout-hash", "", ""); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected the token to no longer authenticate, got %v", err)
	}
}

func TestRevokeIsIdempotent(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	created, err := repo.Create(ctx, user.ID, "hash-idempotent", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Revoke(ctx, created.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := repo.Revoke(ctx, created.ID); err != nil {
		t.Errorf("expected revoking an already-revoked token to be a no-op, got %v", err)
	}
}

func TestRevokeAndUpdateExpiryRejectUnknownID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	if err := repo.Revoke(ctx, 999999); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound revoking an unknown id, got %v", err)
	}
	if err := repo.UpdateExpiry(ctx, 999999, time.Now().Add(time.Hour)); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound updating an unknown id, got %v", err)
	}
	if _, err := repo.GetByID(ctx, 999999); !errors.Is(err, tokens.ErrNotFound) {
		t.Errorf("expected ErrNotFound getting an unknown id, got %v", err)
	}
}

func TestUpdateExpiry(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	created, err := repo.Create(ctx, user.ID, "hash-expiry", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newExpiry := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if err := repo.UpdateExpiry(ctx, created.ID, newExpiry); err != nil {
		t.Fatalf("update expiry: %v", err)
	}
	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if !got.ExpiresAt.Equal(newExpiry) {
		t.Errorf("expected expires_at %v, got %v", newExpiry, got.ExpiresAt)
	}
}

func TestListForUserIncludesRevokedAndOrdersNewestFirst(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, other.ID, "other-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create for other user: %v", err)
	}

	first, err := repo.Create(ctx, user.ID, "first-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := repo.Revoke(ctx, first.ID); err != nil {
		t.Fatalf("revoke first: %v", err)
	}
	second, err := repo.Create(ctx, user.ID, "second-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	list, err := repo.ListForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list for user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 tokens for this user, got %d: %+v", len(list), list)
	}
	if list[0].ID != second.ID || list[1].ID != first.ID {
		t.Errorf("expected newest-first order [%d, %d], got [%d, %d]", second.ID, first.ID, list[0].ID, list[1].ID)
	}
	if list[1].RevokedAt == nil {
		t.Errorf("expected the revoked token to still be listed, with RevokedAt set")
	}
}

// A session remembers the User-Agent of its last use, like the address: both describe the same
// request. The browser and system are worked out from it when the token is read.
func TestAuthenticateRecordsTheLastUserAgent(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := tokens.NewRepository(tx)
	ctx := context.Background()
	user := testutil.InsertUser(t, tx, "")
	created, err := repo.Create(ctx, user.ID, "hash-ua", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.LastUsedUserAgent != nil || created.Browser != "" || created.OS != "" {
		t.Errorf("a new token has no user agent yet: %+v", created)
	}
	read := func() tokens.Token {
		got, err := repo.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return got
	}

	const firefox = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0"
	const safari = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
	if _, _, err := repo.Authenticate(ctx, "hash-ua", "203.0.113.5", firefox); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	got := read()
	if got.LastUsedUserAgent == nil || *got.LastUsedUserAgent != firefox || got.Browser != "Firefox 120" || got.OS != "Linux" {
		t.Errorf("unexpected client after the first use: %+v", got)
	}

	// The last one is the one kept.
	if _, _, err := repo.Authenticate(ctx, "hash-ua", "203.0.113.6", safari); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got := read(); got.Browser != "Safari 17" || got.OS != "iOS" {
		t.Errorf("expected the later request's client, got %+v", got)
	}

	// A request that sends none leaves none, like the address: the row describes the last request.
	if _, _, err := repo.Authenticate(ctx, "hash-ua", "203.0.113.6", ""); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got := read(); got.LastUsedUserAgent != nil || got.Browser != "" {
		t.Errorf("expected no user agent after a request without one, got %+v", got)
	}

	// What a client sends is bounded.
	if _, _, err := repo.Authenticate(ctx, "hash-ua", "", strings.Repeat("é", 2000)); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got := read(); got.LastUsedUserAgent == nil || len([]rune(*got.LastUsedUserAgent)) != 512 {
		t.Errorf("expected the user agent cut to 512 characters, got %d", len([]rune(deref(got.LastUsedUserAgent))))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

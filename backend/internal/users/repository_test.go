package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/users"
)

func TestCreateWithoutAccess(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	u, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "Espeleo", Email: testutil.UniqueEmail(t),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.HasAccess {
		t.Errorf("expected HasAccess to be false when GrantAccess wasn't set")
	}
	if u.ID == 0 {
		t.Errorf("expected a non-zero id")
	}
}

func TestCreateWithAccess(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	phone := "611222333"

	u, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Marta", LastName: "Prueba", Email: testutil.UniqueEmail(t),
		Phone: &phone, GrantAccess: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !u.HasAccess {
		t.Errorf("expected HasAccess to be true when GrantAccess was set")
	}

	account, err := repo.GetAccountByUserID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if !account.IsActive {
		t.Errorf("expected a freshly created account to be active")
	}
}

func TestCreateDuplicateEmailRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{FirstName: "A", LastName: "B", Email: email}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{FirstName: "C", LastName: "D", Email: email})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestCreateDuplicateNationalIDRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	nationalID := "12345678A"

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t), NationalID: &nationalID,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: testutil.UniqueEmail(t), NationalID: &nationalID,
	})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	repo := users.NewRepository(testutil.NewTx(t))
	if _, err := repo.GetByID(context.Background(), 999999); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetByEmail(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	created, err := repo.Create(context.Background(), users.CreateInput{FirstName: "A", LastName: "B", Email: email})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	found, err := repo.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("expected id %d, got %d", created.ID, found.ID)
	}

	if _, err := repo.GetByEmail(context.Background(), "nobody-"+email); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown email, got %v", err)
	}
}

func TestList(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "One", Email: testutil.UniqueEmail(t)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "B", LastName: "Two", Email: testutil.UniqueEmail(t)}); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}
}

func TestFindActiveByEmailAndPhoneMatchesRegardlessOfPhoneFormatting(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600 111 222"), GrantAccess: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600-111-222")
	if err != nil {
		t.Fatalf("expected a match despite different phone formatting, got %v", err)
	}
	if found.Email != email {
		t.Errorf("unexpected match: %+v", found)
	}
}

func TestFindActiveByEmailAndPhoneRejectsWrongPhone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600111222"), GrantAccess: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "000000000"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a mismatched phone, got %v", err)
	}
}

func TestFindActiveByEmailAndPhoneRejectsInactiveAccount(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600111222"), GrantAccess: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `UPDATE accounts SET is_active = false WHERE user_id = $1`, created.ID); err != nil {
		t.Fatalf("deactivate account: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600111222"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a deactivated account, got %v", err)
	}
}

func TestFindActiveByEmailAndPhoneRejectsNoAccountAtAll(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600111222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600111222"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound when the person has no account at all, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newCity := "Alcalá de Henares"
	updated, err := repo.Update(ctx, created.ID, users.UpdateInput{City: &newCity})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.City == nil || *updated.City != newCity {
		t.Errorf("expected city to be updated, got %+v", updated.City)
	}
	if updated.FirstName != "A" {
		t.Errorf("expected untouched fields to survive an update, got first_name %q", updated.FirstName)
	}
}

func TestUpdateEmailToADuplicateIsRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	taken := testutil.UniqueEmail(t)
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: taken}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = repo.Update(ctx, second.ID, users.UpdateInput{Email: &taken})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected the user to be gone, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	repo := users.NewRepository(testutil.NewTx(t))
	if err := repo.Delete(context.Background(), 999999); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteBlockedByReferences(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO activities (title, start_date, location, leader_id) VALUES ('Test trip', now(), 'Somewhere', $1)
	`, created.ID)
	if err != nil {
		t.Fatalf("insert referencing activity: %v", err)
	}

	if err := repo.Delete(ctx, created.ID); !errors.Is(err, users.ErrHasReferences) {
		t.Errorf("expected ErrHasReferences, got %v", err)
	}
}

func TestRefreshTokenRoundTrip(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	if err := repo.SaveRefreshToken(ctx, user.ID, "hash-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save: %v", err)
	}

	gotID, err := repo.GetRefreshToken(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, gotID)
	}

	if err := repo.RevokeRefreshToken(ctx, "hash-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := repo.GetRefreshToken(ctx, "hash-1"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected a revoked token to be rejected, got %v", err)
	}
}

func TestGetRefreshTokenRejectsExpired(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	if err := repo.SaveRefreshToken(ctx, user.ID, "expired-hash", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := repo.GetRefreshToken(ctx, "expired-hash"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected an expired token to be rejected, got %v", err)
	}
}

func TestMagicLinkTokenLifecycle(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)

	if recent, err := repo.HasRecentMagicLinkToken(ctx, user.ID, time.Now().Add(-time.Minute)); err != nil || recent {
		t.Fatalf("expected no recent token yet, got %v (err %v)", recent, err)
	}

	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}

	if recent, err := repo.HasRecentMagicLinkToken(ctx, user.ID, time.Now().Add(-time.Minute)); err != nil || !recent {
		t.Fatalf("expected a recent token to be detected, got %v (err %v)", recent, err)
	}

	gotID, err := repo.ConsumeMagicLinkToken(ctx, "token-hash")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if gotID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, gotID)
	}
}

func TestConsumeMagicLinkTokenRejectsReuse(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "one-shot", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkToken(ctx, "one-shot"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkToken(ctx, "one-shot"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken on reuse, got %v", err)
	}
}

func TestConsumeMagicLinkTokenRejectsExpired(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "expired", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkToken(ctx, "expired"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for an expired link, got %v", err)
	}
}

func TestConsumeMagicLinkTokenRejectsUnknown(t *testing.T) {
	repo := users.NewRepository(testutil.NewTx(t))
	if _, err := repo.ConsumeMagicLinkToken(context.Background(), "never-issued"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestListDirectoryOnlyIncludesPeopleWithAccess(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	withAccess, err := repo.Create(ctx, users.CreateInput{
		FirstName: "Ana", LastName: "Con acceso", Email: testutil.UniqueEmail(t),
		Phone: strPtr("600111222"), GrantAccess: true,
	})
	if err != nil {
		t.Fatalf("create with access: %v", err)
	}
	if _, err := repo.Create(ctx, users.CreateInput{
		FirstName: "Bea", LastName: "Sin acceso", Email: testutil.UniqueEmail(t),
	}); err != nil {
		t.Fatalf("create without access: %v", err)
	}

	list, err := repo.ListDirectory(ctx)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected only the person with access to appear, got %d entries: %+v", len(list), list)
	}
	if list[0].ID != withAccess.ID {
		t.Errorf("expected entry for user %d, got %+v", withAccess.ID, list[0])
	}
}

func strPtr(s string) *string { return &s }

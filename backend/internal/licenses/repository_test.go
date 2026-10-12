package licenses_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCreateComputesValidUntilAsDecember31(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A PLUS")

	l, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, LicenseTypeID: typeID, IssuedDate: date("2026-03-15"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, l.UserID)
	}
	if l.Type != "A PLUS" {
		t.Errorf("expected type %q, got %q", "A PLUS", l.Type)
	}
	if !l.ValidUntil.Equal(date("2026-12-31")) {
		t.Errorf("expected valid_until 2026-12-31, got %v", l.ValidUntil)
	}
}

// Whether other_detail is required or forbidden for a given type is a
// business rule enforced by Service.Create (see service_test.go), not
// the repository — the repository just persists whatever it's given.
func TestCreateWithTheOtraTypeStoresOtherDetailAndComposesTheDisplayType(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	otraID := testutil.LicenseTypeID(t, tx, "Otra")
	detail := "FME"

	l, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, LicenseTypeID: otraID, OtherDetail: &detail, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.OtherDetail == nil || *l.OtherDetail != "FME" {
		t.Errorf("expected other_detail %q, got %v", "FME", l.OtherDetail)
	}
	if l.Type != "Otra: FME" {
		t.Errorf(`expected type "Otra: FME", got %q`, l.Type)
	}
}

func TestUpdate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	typeB1 := testutil.LicenseTypeID(t, tx, "B1 PLUS")

	created, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, LicenseTypeID: typeA, IssuedDate: date("2025-01-01"), Year: 2025, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := repo.Update(context.Background(), created.ID, licenses.UpdateInput{
		LicenseTypeID: typeB1, IssuedDate: date("2026-03-15"), Year: 2026})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Type != "B1 PLUS" {
		t.Errorf("expected updated type %q, got %q", "B1 PLUS", updated.Type)
	}
	if !updated.IssuedDate.Equal(date("2026-03-15")) {
		t.Errorf("expected updated issued_date, got %v", updated.IssuedDate)
	}
	if !updated.ValidUntil.Equal(date("2026-12-31")) {
		t.Errorf("expected recomputed valid_until, got %v", updated.ValidUntil)
	}
}

func TestUpdateNotFound(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")

	_, err := repo.Update(context.Background(), 999999, licenses.UpdateInput{LicenseTypeID: typeA, IssuedDate: date("2026-01-01"), Year: 2026})
	if !errors.Is(err, licenses.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")

	created, err := repo.Create(context.Background(), licenses.CreateInput{UserID: user.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list, err := repo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected the license to be gone, got %+v", list)
	}
}

func TestDeleteNotFound(t *testing.T) {
	repo := licenses.NewRepository(testutil.NewTx(t))
	if err := repo.Delete(context.Background(), 999999); !errors.Is(err, licenses.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByUserOrdersNewestFirstAndScopesToTheUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	typeB1 := testutil.LicenseTypeID(t, tx, "B1 PLUS")
	ctx := context.Background()

	if _, err := repo.Create(ctx, licenses.CreateInput{UserID: other.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
		t.Fatalf("create for other: %v", err)
	}
	older, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, LicenseTypeID: typeA, IssuedDate: date("2025-02-01"), Year: 2025, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, LicenseTypeID: typeB1, IssuedDate: date("2026-02-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}

	list, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 licenses for this user, got %d", len(list))
	}
	if list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("expected newest-first order [%d, %d], got [%d, %d]", newer.ID, older.ID, list[0].ID, list[1].ID)
	}
}

func TestListByUserReturnsEmptySliceNotNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	list, err := repo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
}

func TestListTypesOrdersByInsertionWithOtraLast(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)

	list, err := repo.ListTypes(context.Background())
	if err != nil {
		t.Fatalf("list types: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected a non-empty catalog")
	}
	last := list[len(list)-1]
	if last.Name != "Otra" || !last.IsOther {
		t.Errorf(`expected "Otra" (is_other) last, got %+v`, last)
	}
}

func TestGetTypeByIDNotFound(t *testing.T) {
	repo := licenses.NewRepository(testutil.NewTx(t))
	if _, err := repo.GetTypeByID(context.Background(), 999999); err != licenses.ErrTypeNotFound {
		t.Errorf("expected ErrTypeNotFound, got %v", err)
	}
}

// One payment can cover several socios' licenses: listing a transaction's
// licenses returns all of them, each with its owner's login and type
// label, and none of another transaction's.
func TestListByTransactionReturnsOnlyThatTransactionsLicenses(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	first := testutil.InsertUser(t, tx, "")
	second := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	shared := testutil.InsertTransaction(t, tx)
	for _, person := range []testutil.TestUser{first, second} {
		if _, err := repo.Create(ctx, licenses.CreateInput{
			UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: shared,
		}); err != nil {
			t.Fatalf("create license for %s: %v", person.Login, err)
		}
	}
	if _, err := repo.Create(ctx, licenses.CreateInput{
		UserID: first.ID, LicenseTypeID: typeID, IssuedDate: date("2025-01-01"), Year: 2025, TransactionID: testutil.InsertTransaction(t, tx),
	}); err != nil {
		t.Fatalf("create the other transaction's license: %v", err)
	}

	list, err := repo.ListByTransaction(ctx, shared)
	if err != nil {
		t.Fatalf("ListByTransaction: %v", err)
	}
	logins := map[string]bool{}
	for _, l := range list {
		logins[l.UserLogin] = true
		if l.Year != 2026 || l.TransactionID != shared || l.Type != "A BASICA" {
			t.Errorf("unexpected license in the listing: %+v", l)
		}
	}
	if len(list) != 2 || !logins[first.Login] || !logins[second.Login] {
		t.Errorf("expected exactly the two licenses of the shared transaction, got %+v", list)
	}
}

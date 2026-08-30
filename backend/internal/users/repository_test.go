package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/users"
)

func TestCreate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	phone := "611222333"

	u, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "Espeleo", Email: testutil.UniqueEmail(t), Phone: &phone,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 {
		t.Errorf("expected a non-zero id")
	}
	if u.Phone == nil || *u.Phone != phone {
		t.Errorf("expected phone %q, got %v", phone, u.Phone)
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

func TestCreateAndUpdateGender(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	female := users.GenderFemale

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t), Gender: &female,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Gender == nil || *created.Gender != users.GenderFemale {
		t.Errorf("expected gender %q, got %v", users.GenderFemale, created.Gender)
	}

	male := users.GenderMale
	updated, err := repo.Update(context.Background(), created.ID, users.UpdateInput{Gender: &male})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Gender == nil || *updated.Gender != users.GenderMale {
		t.Errorf("expected gender %q after update, got %v", users.GenderMale, updated.Gender)
	}
}

func TestCreateWithoutGenderLeavesItNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Gender != nil {
		t.Errorf("expected nil gender, got %v", *created.Gender)
	}
}

// membership_number is no longer part of CreateInput/UpdateInput — it's
// assigned by EnsureMembershipNumber, not typed in by hand — but the
// database's uniqueness and non-negative constraints (see migration
// 00012) are still the last line of defense, so exercise them directly.
func TestMembershipNumberMustBeUnique(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	a, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET membership_number = 1 WHERE id = $1`, a.ID); err != nil {
		t.Fatalf("set first number: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET membership_number = 1 WHERE id = $1`, b.ID); err == nil {
		t.Error("expected a duplicate membership_number to be rejected by the database")
	}
}

func TestMembershipNumberCannotBeNegative(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	u, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET membership_number = -1 WHERE id = $1`, u.ID); err == nil {
		t.Error("expected a negative membership_number to be rejected by the database")
	}
}

func TestEnsureMembershipNumberAssignsNextAfterHighest(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	first, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := repo.EnsureMembershipNumber(ctx, first.ID); err != nil {
		t.Fatalf("ensure first: %v", err)
	}
	gotFirst, err := repo.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if gotFirst.MembershipNumber == nil {
		t.Fatal("expected a membership number to be assigned")
	}

	second, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := repo.EnsureMembershipNumber(ctx, second.ID); err != nil {
		t.Fatalf("ensure second: %v", err)
	}
	gotSecond, err := repo.GetByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if gotSecond.MembershipNumber == nil || *gotSecond.MembershipNumber != *gotFirst.MembershipNumber+1 {
		t.Errorf("expected membership number %d, got %v", *gotFirst.MembershipNumber+1, gotSecond.MembershipNumber)
	}
}

// A person's membership number belongs to them, not to any one
// membership period — leaving and rejoining (however many times) must
// never change it, so a second call is a no-op once one is already set.
func TestEnsureMembershipNumberIsANoOpOnceAlreadySet(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	u, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.EnsureMembershipNumber(ctx, u.ID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	first, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if err := repo.EnsureMembershipNumber(ctx, u.ID); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	second, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if *second.MembershipNumber != *first.MembershipNumber {
		t.Errorf("expected membership number to stay %d, got %d", *first.MembershipNumber, *second.MembershipNumber)
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

func TestGetByEmailMatchesAnyOfSeveralEmails(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	primary := testutil.UniqueEmail(t)
	secondary := testutil.UniqueEmail(t)

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: primary + " " + secondary,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.GetByEmail(context.Background(), secondary)
	if err != nil {
		t.Fatalf("get by secondary email: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("expected id %d, got %d", created.ID, found.ID)
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

// A stored phone can't use spaces as internal formatting anymore — that's
// now reserved for separating more than one phone (see
// Repository.FindActiveByEmailAndPhone) — but other punctuation like
// dashes is still just noise, on either side of the comparison.
func TestFindActiveByEmailAndPhoneMatchesRegardlessOfPhoneFormatting(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600-111-222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600 111 222")
	if err != nil {
		t.Fatalf("expected a match despite different phone formatting, got %v", err)
	}
	if found.Email != email {
		t.Errorf("unexpected match: %+v", found)
	}
}

func TestFindActiveByEmailAndPhoneMatchesAnyOfSeveralPhones(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600111222 622333444"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "622-333-444"); err != nil {
		t.Errorf("expected a match against the second phone, got %v", err)
	}
	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600-111-222"); err != nil {
		t.Errorf("expected a match against the first phone, got %v", err)
	}
}

func TestFindActiveByEmailAndPhoneMatchesAnyOfSeveralEmails(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	primary := testutil.UniqueEmail(t)
	secondary := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: primary + " " + secondary, Phone: strPtr("600111222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), secondary, "600111222"); err != nil {
		t.Errorf("expected a match against the secondary email, got %v", err)
	}
}

func TestFindActiveByEmailAndPhoneRejectsWrongPhone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, Phone: strPtr("600111222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "000000000"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a mismatched phone, got %v", err)
	}
}

func TestFindActiveByEmailAndPhoneRejectsWhenNoPhoneOnFile(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: email,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600111222"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound when the person has no phone on file at all, got %v", err)
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

// One person can hold several emails, but each individual address still
// can't belong to two different people — even when it's only one of
// several the new person is being given.
func TestCreateRejectsWhenOneOfSeveralEmailsIsAlreadyTaken(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	taken := testutil.UniqueEmail(t)
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: taken}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := repo.Create(ctx, users.CreateInput{
		FirstName: "C", LastName: "D", Email: testutil.UniqueEmail(t) + " " + taken,
	})
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

func TestMagicLinkTokenLifecycle(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")

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

	user := testutil.InsertUser(t, tx, "")
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

	user := testutil.InsertUser(t, tx, "")
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

// A nil slice marshals to JSON null, not []; the frontend calls .map()/
// .length on the response, so an empty result must come back as [] or
// the page crashes trying to render it.
func TestListReturnsEmptySliceNotNilWhenThereAreNoUsers(t *testing.T) {
	repo := users.NewRepository(testutil.NewTx(t))
	list, err := repo.List(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
	if len(list) != 0 {
		t.Errorf("expected no users in a fresh transaction, got %+v", list)
	}
}

// Being a directory entry never required being able to sign in — an
// activity participant or the party to a material transaction doesn't
// need login access at all, so everyone on file shows up here.
func TestListDirectoryIncludesEveryone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	withPhone, err := repo.Create(ctx, users.CreateInput{
		FirstName: "Ana", LastName: "Con teléfono", Email: testutil.UniqueEmail(t),
		Phone: strPtr("600111222"),
	})
	if err != nil {
		t.Fatalf("create with phone: %v", err)
	}
	withoutPhone, err := repo.Create(ctx, users.CreateInput{
		FirstName: "Bea", LastName: "Sin teléfono", Email: testutil.UniqueEmail(t),
	})
	if err != nil {
		t.Fatalf("create without phone: %v", err)
	}

	list, err := repo.ListDirectory(ctx)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected both people to appear, got %d entries: %+v", len(list), list)
	}
	ids := map[int64]bool{list[0].ID: true, list[1].ID: true}
	if !ids[withPhone.ID] || !ids[withoutPhone.ID] {
		t.Errorf("expected both %d and %d, got %+v", withPhone.ID, withoutPhone.ID, list)
	}
}

func strPtr(s string) *string { return &s }

package users_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func TestCreate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	phone := "611222333"

	u, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "Espeleo", Email: strPtr(testutil.UniqueEmail(t)), Phone: &phone,
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

// TestCreateAppendsNumericSuffixOnLoginCollision covers generateLogin's
// own collision path: two people whose names slugify to the exact same
// base login ("juan.perez") don't fail — the second gets that same
// base with a numeric suffix appended (loginWithSuffix), the first
// still-free candidate in that sequence.
func TestCreateAppendsNumericSuffixOnLoginCollision(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	first, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Juan", LastName: "Pérez", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.Login != "juan.perez" {
		t.Fatalf("expected the first person to get the plain base login, got %q", first.Login)
	}

	second, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Juan", LastName: "Pérez", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.Login != "juan.perez2" {
		t.Errorf("expected the second person's login to get a numeric suffix, got %q", second.Login)
	}

	third, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "Juan", LastName: "Pérez", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create third: %v", err)
	}
	if third.Login != "juan.perez3" {
		t.Errorf("expected the third person's login to move on to the next free suffix, got %q", third.Login)
	}
}

func TestCreateDuplicateEmailRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(email)}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{FirstName: "C", LastName: "D", Email: strPtr(email)})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

// TestCreateDuplicateEmailRejectedRegardlessOfCasing guards against a
// real gap that used to exist here: emailTakenByAnotherUser's own
// duplicate check used to compare emails exactly, so "foo@x.com" and
// "FOO@X.COM" could end up belonging to two different people even
// though FindActiveByEmailAndPhone's login lookup already treats them
// as the same address — letting a login-relevant duplicate slip past
// the one check meant to prevent it.
func TestCreateDuplicateEmailRejectedRegardlessOfCasing(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(email)}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{FirstName: "C", LastName: "D", Email: strPtr(strings.ToUpper(email))})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate despite the different casing, got %v", err)
	}
}

// TestCreateDuplicatePhoneRejected: an individual phone number is
// expected to identify one person, since internal/telegrambot resolves
// a shared Telegram contact straight to whoever its phone number
// belongs to (Repository.FindByPhoneNumber) — two people sharing one
// would make that resolution ambiguous, the same reason email's own
// duplicate check exists.
func TestCreateDuplicatePhoneRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	phone := "611222333"

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: &phone,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t)), Phone: &phone,
	})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

// TestCreateDuplicatePhoneRejectedRegardlessOfFormatting matches
// Repository.FindActiveByEmailAndPhone's own digits-only comparison —
// two phone numbers that are the same once punctuation is stripped are
// the same phone number, not two different ones that merely look alike.
func TestCreateDuplicatePhoneRejectedRegardlessOfFormatting(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611-222-333"),
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate despite the different formatting, got %v", err)
	}
}

// TestCreateDuplicatePhoneRejectedAgainstAnyOfSeveral covers the
// multi-phone convention (phone can hold more than one, space-separated
// — see users.User.Phone): a new person's phone colliding with just one
// of an existing person's several is still a collision.
func TestCreateDuplicatePhoneRejectedAgainstAnyOfSeveral(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("600111222 622333444"),
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("622333444"),
	})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate against the second of the existing person's phones, got %v", err)
	}
}

func TestCreateDuplicateNationalIDRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	nationalID := "12345678A"

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), NationalID: &nationalID,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t)), NationalID: &nationalID,
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
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Gender: &female,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Gender == nil || *created.Gender != users.GenderFemale {
		t.Errorf("expected gender %q, got %v", users.GenderFemale, created.Gender)
	}

	male := users.GenderMale
	updated, err := repo.Update(context.Background(), created.ID, users.UpdateInput{Gender: users.Nullable[users.Gender]{Set: true, Value: &male}})
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
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Gender != nil {
		t.Errorf("expected nil gender, got %v", *created.Gender)
	}
}

// membership_number is not part of CreateInput/UpdateInput — it's
// assigned by EnsureMembershipNumber, not typed in by hand — but the
// database's uniqueness and non-negative constraints (see migration
// 00002) are still the last line of defense, so exercise them directly.
func TestMembershipNumberMustBeUnique(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	a, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t))})
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

	u, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
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

	first, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
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

	second, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t))})
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

	u, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
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

	created, err := repo.Create(context.Background(), users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(email)})
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
		FirstName: "A", LastName: "B", Email: strPtr(primary + " " + secondary),
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

	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "One", Email: strPtr(testutil.UniqueEmail(t))}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "B", LastName: "Two", Email: strPtr(testutil.UniqueEmail(t))}); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, false)
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
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("600-111-222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600 111 222")
	if err != nil {
		t.Fatalf("expected a match despite different phone formatting, got %v", err)
	}
	if found.Email == nil || *found.Email != email {
		t.Errorf("unexpected match: %+v", found)
	}
}

func TestFindActiveByEmailAndPhoneMatchesAnyOfSeveralPhones(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("600111222 622333444"),
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
		FirstName: "A", LastName: "B", Email: strPtr(primary + " " + secondary), Phone: strPtr("600111222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), secondary, "600111222"); err != nil {
		t.Errorf("expected a match against the secondary email, got %v", err)
	}
}

// TestFindActiveByEmailAndPhoneMatchesRegardlessOfEmailCasing mirrors
// internal/telegrambot's own emailMatchesUser check: a login that only
// works from one of those two channels and not the other, just because
// of casing, isn't a real security boundary — email addresses aren't
// meant to be case-sensitive.
func TestFindActiveByEmailAndPhoneMatchesRegardlessOfEmailCasing(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("600111222"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindActiveByEmailAndPhone(context.Background(), strings.ToUpper(email), "600111222")
	if err != nil {
		t.Fatalf("expected a match despite different email casing, got %v", err)
	}
	if found.Email == nil || *found.Email != email {
		t.Errorf("unexpected match: %+v", found)
	}
}

func TestFindActiveByEmailAndPhoneRejectsWrongPhone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("600111222"),
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
		FirstName: "A", LastName: "B", Email: strPtr(email),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindActiveByEmailAndPhone(context.Background(), email, "600111222"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound when the person has no phone on file at all, got %v", err)
	}
}

func TestFindByPhoneNumberMatchesAPhoneWithACountryCodePrefix(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := repo.FindByPhoneNumber(context.Background(), "34611222333")
	if err != nil {
		t.Fatalf("expected a match despite the extra country code, got %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("matched the wrong person: %+v", found)
	}
}

func TestFindByPhoneNumberMatchesAnyOfSeveralPhones(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("600111222 622333444"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindByPhoneNumber(context.Background(), "34622333444"); err != nil {
		t.Errorf("expected a match against the second phone, got %v", err)
	}
}

func TestFindByPhoneNumberRejectsWrongPhone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindByPhoneNumber(context.Background(), "34600000000"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a mismatched phone, got %v", err)
	}
}

func TestFindByPhoneNumberRejectsEmptyDigits(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)

	if _, err := repo.FindByPhoneNumber(context.Background(), ""); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for empty digits, got %v", err)
	}
}

// TestFindByPhoneNumberRejectsAForeignNumberSharingTheSameDigits guards
// against the exact impersonation a same-length-agnostic suffix match
// used to allow: a French "+33666777888" and a Spanish "666777888" share
// every digit except the country code, so someone sharing the French
// number with the bot must not be matched to the Spanish person on file.
func TestFindByPhoneNumberRejectsAForeignNumberSharingTheSameDigits(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	if _, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("666777888"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.FindByPhoneNumber(context.Background(), "33666777888"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a French +33 number sharing the Spanish person's digits, got %v", err)
	}
}

func TestSetTelegramChatIDAndUserIDForTelegramChatID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	email := testutil.UniqueEmail(t)

	created, err := repo.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.UserIDForTelegramChatID(context.Background(), 999); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unlinked chat, got %v", err)
	}

	if err := repo.SetTelegramChatID(context.Background(), created.ID, 999); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	userID, err := repo.UserIDForTelegramChatID(context.Background(), 999)
	if err != nil {
		t.Fatalf("UserIDForTelegramChatID: %v", err)
	}
	if userID != created.ID {
		t.Errorf("expected user id %d, got %d", created.ID, userID)
	}
}

func TestUpdate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newCity := "Alcalá de Henares"
	updated, err := repo.Update(ctx, created.ID, users.UpdateInput{City: users.Nullable[string]{Set: true, Value: &newCity}})
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

// TestUpdateRenamesLogin covers the admin-only login correction path
// (UpdateInput.Login) — auto-generated at creation, but editable
// afterward, unlike every other identity field it's dot-joined from.
func TestUpdateRenamesLogin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "Juan", LastName: "Pérez", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Login != "juan.perez" {
		t.Fatalf("expected the auto-generated login, got %q", created.Login)
	}

	newLogin := "juanpe"
	updated, err := repo.Update(ctx, created.ID, users.UpdateInput{Login: &newLogin})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Login != newLogin {
		t.Errorf("expected login %q, got %q", newLogin, updated.Login)
	}
}

// TestUpdateRejectsInvalidLoginFormat covers loginFormat's own
// validation on a hand-edited login — spaces, underscores, and a
// leading/doubled separator are all rejected (uppercase isn't one of
// these cases: Update lowercases before validating, the same
// case-insensitive normalization login lookups elsewhere already use,
// so "Juan.Perez" is accepted as "juan.perez" rather than rejected).
func TestUpdateRejectsInvalidLoginFormat(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, bad := range []string{"juan perez", "juan_perez", ".juanperez", "juan--perez"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			if _, err := repo.Update(ctx, created.ID, users.UpdateInput{Login: &bad}); !errors.Is(err, users.ErrInvalidLogin) {
				t.Errorf("expected ErrInvalidLogin for %q, got %v", bad, err)
			}
		})
	}
}

// TestUpdateRejectsDuplicateLogin covers renaming into a login another
// person already has.
func TestUpdateRejectsDuplicateLogin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	existing, err := repo.Create(ctx, users.CreateInput{FirstName: "Ana", LastName: "Gomez", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create existing: %v", err)
	}
	target, err := repo.Create(ctx, users.CreateInput{FirstName: "Luis", LastName: "Ruiz", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	if _, err := repo.Update(ctx, target.ID, users.UpdateInput{Login: &existing.Login}); !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

// TestUpdateClearsAnOptionalFieldWhenExplicitlySentAsNull reproduces the
// reported bug: PATCH said it worked, but a cleared phone (or any other
// optional field) stayed on file, because a zero-value UpdateInput field
// was indistinguishable from one whose key was simply never in the body.
func TestUpdateClearsAnOptionalFieldWhenExplicitlySentAsNull(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := repo.Update(ctx, created.ID, users.UpdateInput{
		Phone: users.Nullable[string]{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Phone != nil {
		t.Errorf("expected phone to be cleared, still got %q", *updated.Phone)
	}

	// Fetching fresh confirms it was actually persisted, not just
	// reflected in the in-memory return value.
	reloaded, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if reloaded.Phone != nil {
		t.Errorf("expected phone to stay cleared after reloading, got %q", *reloaded.Phone)
	}
}

// TestUpdateLeavesAnOptionalFieldAloneWhenItsKeyIsAbsent is the other
// half of the same fix: a field that's simply not part of the PATCH
// body (Set left false) must still survive untouched, the way it always
// did — only an explicit null clears it.
func TestUpdateLeavesAnOptionalFieldAloneWhenItsKeyIsAbsent(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newCity := "Alcalá de Henares"
	updated, err := repo.Update(ctx, created.ID, users.UpdateInput{
		City: users.Nullable[string]{Set: true, Value: &newCity},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Phone == nil || *updated.Phone != "611222333" {
		t.Errorf("expected phone to survive untouched since its key was absent, got %+v", updated.Phone)
	}
}

// TestUpdateChangingPhoneClearsLinkedTelegramChatID: a linked
// telegram_chat_id was only ever established by internal/telegrambot
// matching a shared contact's phone number against this exact one
// (users.Repository.FindByPhoneNumber) — once the phone on file changes,
// that link no longer proves anything and must not survive, or someone
// who kept the old phone number could still pass the bot's phone check
// for an account it no longer belongs to.
func TestUpdateChangingPhoneClearsLinkedTelegramChatID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SetTelegramChatID(ctx, created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	newPhone := "699888777"
	if _, err := repo.Update(ctx, created.ID, users.UpdateInput{
		Phone: users.Nullable[string]{Set: true, Value: &newPhone},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	chatID, err := repo.TelegramChatIDForUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("TelegramChatIDForUser: %v", err)
	}
	if chatID != nil {
		t.Errorf("expected telegram_chat_id to be cleared after changing phone, got %v", *chatID)
	}
}

// TestUpdateClearingPhoneClearsLinkedTelegramChatID covers the same
// rationale as TestUpdateChangingPhoneClearsLinkedTelegramChatID for the
// explicit-null case (phone removed entirely, not just replaced).
func TestUpdateClearingPhoneClearsLinkedTelegramChatID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SetTelegramChatID(ctx, created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if _, err := repo.Update(ctx, created.ID, users.UpdateInput{
		Phone: users.Nullable[string]{Set: true, Value: nil},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	chatID, err := repo.TelegramChatIDForUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("TelegramChatIDForUser: %v", err)
	}
	if chatID != nil {
		t.Errorf("expected telegram_chat_id to be cleared after clearing phone, got %v", *chatID)
	}
}

// TestUpdateLeavesLinkedTelegramChatIDAloneWhenPhoneIsUnchanged makes
// sure the clearing above is specifically tied to the phone actually
// changing — an update that doesn't touch it (or resends the same
// value) must not silently unlink an already-verified Telegram chat.
func TestUpdateLeavesLinkedTelegramChatIDAloneWhenPhoneIsUnchanged(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.SetTelegramChatID(ctx, created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	newCity := "Alcalá de Henares"
	samePhone := "611222333"
	if _, err := repo.Update(ctx, created.ID, users.UpdateInput{
		City:  users.Nullable[string]{Set: true, Value: &newCity},
		Phone: users.Nullable[string]{Set: true, Value: &samePhone},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	chatID, err := repo.TelegramChatIDForUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("TelegramChatIDForUser: %v", err)
	}
	if chatID == nil || *chatID != 555 {
		t.Errorf("expected telegram_chat_id to survive an update that doesn't change phone, got %v", chatID)
	}
}

func TestUpdateEmailToADuplicateIsRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	taken := testutil.UniqueEmail(t)
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(taken)}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := repo.Create(ctx, users.CreateInput{FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = repo.Update(ctx, second.ID, users.UpdateInput{Email: users.Nullable[string]{Set: true, Value: &taken}})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestUpdatePhoneToADuplicateIsRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	taken := "611222333"
	if _, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: &taken,
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := repo.Create(ctx, users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = repo.Update(ctx, second.ID, users.UpdateInput{Phone: users.Nullable[string]{Set: true, Value: &taken}})
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
	if _, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(taken)}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := repo.Create(ctx, users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(testutil.UniqueEmail(t) + " " + taken),
	})
	if !errors.Is(err, users.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
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

	created, err := repo.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO activities (title, start_date, end_date, leader_id) VALUES ('Test trip', now(), now(), $1)
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

	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
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
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "one-shot", "one-shot-code", time.Now().Add(15*time.Minute)); err != nil {
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
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "expired", "expired-code", time.Now().Add(-time.Minute)); err != nil {
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

func TestConsumeMagicLinkCode(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}

	gotID, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash")
	if err != nil {
		t.Fatalf("consume code: %v", err)
	}
	if gotID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, gotID)
	}
}

// Consuming the code also burns the link, and vice versa — they
// represent the same sign-in attempt, one row.
func TestConsumeMagicLinkCodeAlsoConsumesTheLink(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash"); err != nil {
		t.Fatalf("consume code: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkToken(ctx, "token-hash"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected the link to be consumed too, got %v", err)
	}
}

func TestConsumeMagicLinkCodeRejectsReuse(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash"); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken on reuse, got %v", err)
	}
}

func TestConsumeMagicLinkCodeRejectsExpired(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for an expired code, got %v", err)
	}
}

func TestConsumeMagicLinkCodeRejectsWrongCode(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "wrong-code"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for a wrong code, got %v", err)
	}
}

func TestConsumeMagicLinkCodeDoesNotMatchAnotherUsersRow(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	owner := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, owner.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.ConsumeMagicLinkCode(ctx, other.ID, "code-hash"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken when the code belongs to a different user, got %v", err)
	}
}

func TestConsumeMagicLinkCodeLocksOutAfterTooManyWrongGuesses(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	if err := repo.CreateMagicLinkToken(ctx, user.ID, "token-hash", "code-hash", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Exhaust the attempt budget with wrong guesses.
	for i := 0; i < 8; i++ {
		if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "wrong-code"); !errors.Is(err, users.ErrInvalidToken) {
			t.Fatalf("attempt %d: expected ErrInvalidToken, got %v", i, err)
		}
	}

	// Even the correct code no longer works once locked out.
	if _, err := repo.ConsumeMagicLinkCode(ctx, user.ID, "code-hash"); !errors.Is(err, users.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken once the attempt budget is exhausted, got %v", err)
	}

	// The link itself is unaffected by the code being locked out.
	if _, err := repo.ConsumeMagicLinkToken(ctx, "token-hash"); err != nil {
		t.Errorf("expected the link to still work despite the code being locked out, got %v", err)
	}
}

// A nil slice marshals to JSON null, not []; the frontend calls .map()/
// .length on the response, so an empty result must come back as [] or
// the page crashes trying to render it.
func TestListReturnsEmptySliceNotNilWhenThereAreNoUsers(t *testing.T) {
	repo := users.NewRepository(testutil.NewTx(t))
	list, err := repo.List(context.Background(), false)
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
		FirstName: "Ana", LastName: "Con teléfono", Email: strPtr(testutil.UniqueEmail(t)),
		Phone: strPtr("600111222"),
	})
	if err != nil {
		t.Fatalf("create with phone: %v", err)
	}
	withoutPhone, err := repo.Create(ctx, users.CreateInput{
		FirstName: "Bea", LastName: "Sin teléfono", Email: strPtr(testutil.UniqueEmail(t)),
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
	logins := map[string]bool{list[0].Login: true, list[1].Login: true}
	if !logins[withPhone.Login] || !logins[withoutPhone.Login] {
		t.Errorf("expected both %q and %q, got %+v", withPhone.Login, withoutPhone.Login, list)
	}
}

// People are listed by name, as they are read ("Nombre Apellidos"), not by
// surname: the directory and the people list feed every picker and list.
func TestPeopleAreListedByNameNotBySurname(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()
	// First names go Ana, Bea, Carlos; the surnames go the other way round.
	for _, p := range []struct{ first, last string }{{"Carlos", "Álvarez"}, {"Ana", "Zapata"}, {"Bea", "Martín"}} {
		if _, err := repo.Create(ctx, users.CreateInput{FirstName: p.first, LastName: p.last, Email: strPtr(testutil.UniqueEmail(t))}); err != nil {
			t.Fatalf("create %s: %v", p.first, err)
		}
	}

	names := func(first []string) string { return strings.Join(first, ",") }
	directory, err := repo.ListDirectory(ctx)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	var got []string
	for _, e := range directory {
		got = append(got, e.FirstName)
	}
	if names(got) != "Ana,Bea,Carlos" {
		t.Errorf("the directory should be in order of first name, got %v", got)
	}
	list, err := repo.List(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got = nil
	for _, u := range list {
		got = append(got, u.FirstName)
	}
	if names(got) != "Ana,Bea,Carlos" {
		t.Errorf("the people list should be in order of first name, got %v", got)
	}
}

func strPtr(s string) *string { return &s }

// A leading "+34" typed on the login page is ignored — the club keeps its
// numbers without it — whatever the spacing after it. Only a literal "+34"
// is: a bare "34" may be the start of a number, and a number that merely
// ends with the right digits isn't a match. A phone stored with its own
// "+34" still matches when typed with it, as it always did.
func TestFindActiveByEmailAndPhoneIgnoresALeadingPlus34(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	ctx := context.Background()
	email := testutil.UniqueEmail(t)
	if _, err := repo.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	withPrefix := testutil.UniqueEmail(t)
	if _, err := repo.Create(ctx, users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(withPrefix), Phone: strPtr("+34655444333"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, typed := range []string{"611222333", "+34611222333", "+34 611 222 333", " +34 611-222-333 "} {
		if _, err := repo.FindActiveByEmailAndPhone(ctx, email, typed); err != nil {
			t.Errorf("expected %q to match 611222333, got %v", typed, err)
		}
	}
	for _, typed := range []string{"34611222333", "+34", "+34 000 000 000", "+33611222333", "+34 +34 611222333"} {
		if _, err := repo.FindActiveByEmailAndPhone(ctx, email, typed); !errors.Is(err, users.ErrNotFound) {
			t.Errorf("expected %q not to match 611222333, got %v", typed, err)
		}
	}
	for _, typed := range []string{"+34655444333", "+34 655 444 333"} {
		if _, err := repo.FindActiveByEmailAndPhone(ctx, withPrefix, typed); err != nil {
			t.Errorf("expected %q to match the phone stored as +34655444333, got %v", typed, err)
		}
	}
}

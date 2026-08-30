package memberships_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/testutil"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCreateMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	number := "0001"

	m, err := repo.Create(context.Background(), memberships.CreateInput{
		UserID: user.ID, MembershipNumber: &number, StartDate: date("2020-01-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, m.UserID)
	}
}

func TestCreateRejectsOverlappingPeriod(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	ctx := context.Background()

	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// An open-ended period (no end date) overlaps any later start date.
	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2021-06-01")})
	if !errors.Is(err, memberships.ErrOverlap) {
		t.Errorf("expected ErrOverlap, got %v", err)
	}
}

func TestCreateAllowsNonOverlappingPeriodsAfterEnding(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	ctx := context.Background()

	first, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstEnd := date("2020-12-31")
	if _, err := repo.Update(ctx, first.ID, memberships.UpdateInput{StartDate: first.StartDate, EndDate: &firstEnd}); err != nil {
		t.Fatalf("end: %v", err)
	}

	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2021-01-01")}); err != nil {
		t.Errorf("expected a period starting after the first one ended to be accepted, got %v", err)
	}
}

func TestUpdateCorrectsDatesAndCanReopenAClosedPeriod(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	ctx := context.Background()

	original, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Correct a wrong start date and (mistakenly) end it.
	wrongEnd := date("2020-06-01")
	updated, err := repo.Update(ctx, original.ID, memberships.UpdateInput{StartDate: date("2020-02-01"), EndDate: &wrongEnd})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.StartDate.Equal(date("2020-02-01")) {
		t.Errorf("expected start date to be corrected, got %v", updated.StartDate)
	}
	if updated.EndDate == nil || !updated.EndDate.Equal(wrongEnd) {
		t.Errorf("expected end date to be set, got %v", updated.EndDate)
	}

	// Realize the "baja" was a mistake and reopen the period by omitting EndDate.
	reopened, err := repo.Update(ctx, original.ID, memberships.UpdateInput{StartDate: date("2020-02-01")})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.EndDate != nil {
		t.Errorf("expected the end date to be cleared, got %v", reopened.EndDate)
	}
}

func TestUpdateRejectsOverlap(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	ctx := context.Background()

	first, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	firstEnd := date("2020-06-01")
	if _, err := repo.Update(ctx, first.ID, memberships.UpdateInput{StartDate: first.StartDate, EndDate: &firstEnd}); err != nil {
		t.Fatalf("end first: %v", err)
	}
	second, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2021-01-01")})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Stretching the second period's start back into the first's range should
	// be rejected by the same exclusion constraint Create relies on.
	if _, err := repo.Update(ctx, second.ID, memberships.UpdateInput{StartDate: date("2020-03-01")}); !errors.Is(err, memberships.ErrOverlap) {
		t.Errorf("expected ErrOverlap, got %v", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	repo := memberships.NewRepository(testutil.NewTx(t))
	endDate := date("2024-01-01")
	_, err := repo.Update(context.Background(), 999999, memberships.UpdateInput{StartDate: date("2020-01-01"), EndDate: &endDate})
	if !errors.Is(err, memberships.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)
	other := testutil.InsertUser(t, tx, "", false)
	ctx := context.Background()

	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create for user: %v", err)
	}
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: other.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create for other: %v", err)
	}

	list, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 membership for this user, got %d", len(list))
	}
}

// A nil slice marshals to JSON null, not []; the frontend calls .map()/
// .length on the response unconditionally, so an empty result must come
// back as [] or the page crashes trying to render it.
func TestListActiveReturnsEmptySliceNotNilWhenNothingIsActive(t *testing.T) {
	repo := memberships.NewRepository(testutil.NewTx(t))
	list, err := repo.ListActive(context.Background())
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
	if len(list) != 0 {
		t.Errorf("expected no active memberships in a fresh transaction, got %+v", list)
	}
}

func TestListByUserReturnsEmptySliceNotNilWhenUserHasNoHistory(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)

	list, err := repo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
	if len(list) != 0 {
		t.Errorf("expected no membership history for a brand-new user, got %+v", list)
	}
}

func TestListActiveIncludesNameAndOnlyCurrentlyOpenPeriods(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	ctx := context.Background()

	active := testutil.InsertUser(t, tx, "", false)
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: active.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create active: %v", err)
	}

	past := testutil.InsertUser(t, tx, "", false)
	pastMembership, err := repo.Create(ctx, memberships.CreateInput{UserID: past.ID, StartDate: date("2010-01-01")})
	if err != nil {
		t.Fatalf("create past: %v", err)
	}
	pastEnd := date("2011-01-01")
	if _, err := repo.Update(ctx, pastMembership.ID, memberships.UpdateInput{StartDate: pastMembership.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end past: %v", err)
	}

	list, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 active membership, got %d: %+v", len(list), list)
	}
	if list[0].UserID != active.ID {
		t.Errorf("expected the active user, got %d", list[0].UserID)
	}
	if list[0].FirstName == "" || list[0].LastName == "" {
		t.Errorf("expected the active list to include the person's name, got %+v", list[0])
	}
}

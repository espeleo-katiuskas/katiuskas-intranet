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
	if _, err := repo.End(ctx, first.ID, memberships.EndInput{EndDate: date("2020-12-31")}); err != nil {
		t.Fatalf("end: %v", err)
	}

	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2021-01-01")}); err != nil {
		t.Errorf("expected a period starting after the first one ended to be accepted, got %v", err)
	}
}

func TestEndNotFound(t *testing.T) {
	repo := memberships.NewRepository(testutil.NewTx(t))
	_, err := repo.End(context.Background(), 999999, memberships.EndInput{EndDate: date("2024-01-01")})
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
	if _, err := repo.End(ctx, pastMembership.ID, memberships.EndInput{EndDate: date("2011-01-01")}); err != nil {
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

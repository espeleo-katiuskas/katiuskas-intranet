package memberships_test

import (
	"context"
	"testing"

	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/testutil"
)

func TestLatestByUserReturnsNilWhenNeverAMember(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")

	latest, err := svc.LatestByUser(context.Background(), person.ID)
	if err != nil {
		t.Fatalf("latest by user: %v", err)
	}
	if latest != nil {
		t.Errorf("expected nil, got %+v", latest)
	}
}

func TestLatestByUserReturnsTheMostRecentPeriod(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := svc.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2018-01-01")}); err != nil {
		t.Fatalf("create first period: %v", err)
	}
	if _, err := svc.Update(ctx, mustLatestID(t, svc, person.ID), memberships.UpdateInput{
		StartDate: date("2018-01-01"), EndDate: ptr(date("2019-01-01")),
	}); err != nil {
		t.Fatalf("end first period: %v", err)
	}
	second, err := svc.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create second period: %v", err)
	}

	latest, err := svc.LatestByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("latest by user: %v", err)
	}
	if latest == nil || latest.ID != second.ID {
		t.Fatalf("expected the second (most recent) period, got %+v", latest)
	}
	if latest.EndDate != nil {
		t.Errorf("expected the current period to still be open, got end date %v", latest.EndDate)
	}
}

// fakeNumberAssigner stands in for users.Service in tests that only care
// whether Create calls the assigner, not what it actually does — the
// real number-assignment behavior is covered end to end in the users
// package's own tests.
type fakeNumberAssigner struct {
	calledWith []int64
}

func (f *fakeNumberAssigner) EnsureMembershipNumber(_ context.Context, userID int64) error {
	f.calledWith = append(f.calledWith, userID)
	return nil
}

func TestCreateCallsTheNumberAssignerWhenSet(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	assigner := &fakeNumberAssigner{}
	svc.SetNumberAssigner(assigner)
	person := testutil.InsertUser(t, tx, "")

	if _, err := svc.Create(context.Background(), memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(assigner.calledWith) != 1 || assigner.calledWith[0] != person.ID {
		t.Errorf("expected EnsureMembershipNumber to be called once with user id %d, got %v", person.ID, assigner.calledWith)
	}
}

func TestCreateWorksFineWithNoNumberAssignerSet(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")

	if _, err := svc.Create(context.Background(), memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Errorf("expected Create to succeed without a number assigner wired in, got %v", err)
	}
}

func mustLatestID(t *testing.T, svc *memberships.Service, userID int64) int64 {
	t.Helper()
	list, err := svc.ListByUser(context.Background(), userID)
	if err != nil || len(list) == 0 {
		t.Fatalf("list by user: %v (len %d)", err, len(list))
	}
	return list[0].ID
}

func ptr[T any](v T) *T {
	return &v
}

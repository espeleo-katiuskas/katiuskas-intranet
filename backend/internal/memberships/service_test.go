package memberships_test

import (
	"context"
	"testing"

	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/testutil"
)

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

func ptr[T any](v T) *T {
	return &v
}

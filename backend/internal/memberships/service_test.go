package memberships_test

import (
	"context"
	"errors"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
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

// TestRecordDueStartsAMembershipForSomeoneWithNone covers the auto-alta
// case: a brand-new socio's very first due (matrícula + current year's
// cuota) also opens their membership period, dated from the payment's
// own occurredOn.
func TestRecordDueStartsAMembershipForSomeoneWithNone(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	d, err := svc.RecordDue(ctx, person.ID, 2026, testutil.InsertTransaction(t, tx), date("2026-03-01"))
	if err != nil {
		t.Fatalf("record due: %v", err)
	}
	if d.UserID != person.ID || d.Year != 2026 {
		t.Errorf("expected user id %d and year 2026, got %+v", person.ID, d)
	}

	list, err := svc.ListByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 1 || !list[0].StartDate.Equal(date("2026-03-01")) {
		t.Errorf("expected a membership started on 2026-03-01, got %+v", list)
	}
}

// TestRecordDueDoesNotStartASecondMembershipForAnExistingMember covers
// the other side: someone who already has a membership period (open or
// closed) on file isn't given a second one just because they're paying a
// due — that's only ever automatic for a genuinely first-time socio.
func TestRecordDueDoesNotStartASecondMembershipForAnExistingMember(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := svc.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	if _, err := svc.RecordDue(ctx, person.ID, 2026, testutil.InsertTransaction(t, tx), date("2026-03-01")); err != nil {
		t.Fatalf("record due: %v", err)
	}

	list, err := svc.ListByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected the existing membership to be left alone, got %+v", list)
	}
}

func TestRecordDueRejectsASecondOneForTheSameYear(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := svc.RecordDue(ctx, person.ID, 2026, testutil.InsertTransaction(t, tx), date("2026-03-01")); err != nil {
		t.Fatalf("record first due: %v", err)
	}
	_, err := svc.RecordDue(ctx, person.ID, 2026, testutil.InsertTransaction(t, tx), date("2026-03-01"))
	if !errors.Is(err, memberships.ErrDueAlreadyRecorded) {
		t.Errorf("expected ErrDueAlreadyRecorded, got %v", err)
	}
}

func TestRecordDueRejectsAnUnknownTransaction(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")

	_, err := svc.RecordDue(context.Background(), person.ID, 2026, 999999999, date("2026-03-01"))
	if !errors.Is(err, memberships.ErrUnknownTransaction) {
		t.Errorf("expected ErrUnknownTransaction, got %v", err)
	}
}

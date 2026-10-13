package memberships_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

// A due can only be tied to a transaction that touches an account marked
// allows_dues (migration 00017): one that only has an account for
// licenses, or for neither, is rejected.
func TestRecordDueRejectsATransactionWithoutADuesAccount(t *testing.T) {
	for _, c := range []struct {
		name               string
		allowsDues, allows bool
	}{
		{"an account for licenses only", false, true},
		{"no flagged account at all", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tx := testutil.NewTx(t)
			svc := memberships.NewService(memberships.NewRepository(tx))
			person := testutil.InsertUser(t, tx, "")

			_, err := svc.RecordDue(context.Background(), person.ID, 2026,
				testutil.InsertTransactionOn(t, tx, c.allowsDues, c.allows), date("2026-03-01"))
			if !errors.Is(err, memberships.ErrTransactionNotForDues) {
				t.Errorf("expected ErrTransactionNotForDues, got %v", err)
			}
		})
	}
}

func TestRecordDueAcceptsATransactionOnADuesAccountThatIsNotAlsoForLicenses(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")

	if _, err := svc.RecordDue(context.Background(), person.ID, 2026,
		testutil.InsertTransactionOn(t, tx, true, false), date("2026-03-01")); err != nil {
		t.Errorf("expected a dues-only account to be enough, got %v", err)
	}
}

// A rejected due must not leave anything behind: the automatic alta for a
// socio with no membership yet only happens once the transaction itself
// has been accepted.
func TestRecordDueRejectedForItsTransactionStartsNoMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := svc.RecordDue(ctx, person.ID, 2026, testutil.InsertTransactionOn(t, tx, false, true), date("2026-03-01")); !errors.Is(err, memberships.ErrTransactionNotForDues) {
		t.Fatalf("expected ErrTransactionNotForDues, got %v", err)
	}
	if _, err := svc.RecordDue(ctx, person.ID, 2026, 999999999, date("2026-03-01")); !errors.Is(err, memberships.ErrUnknownTransaction) {
		t.Fatalf("expected ErrUnknownTransaction, got %v", err)
	}

	list, err := svc.ListByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected no membership to have been started by a rejected due, got %+v", list)
	}
}

// start_date is only needed to open a first membership period: someone
// who already has one can have a due recorded without it, and someone who
// doesn't is asked for it — with nothing started as a side effect.
func TestRecordDueOnlyNeedsAStartDateForSomeoneWithNoMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := memberships.NewService(memberships.NewRepository(tx))
	ctx := context.Background()

	member := testutil.InsertUser(t, tx, "")
	if _, err := svc.Create(ctx, memberships.CreateInput{UserID: member.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := svc.RecordDue(ctx, member.ID, 2026, testutil.InsertTransaction(t, tx), time.Time{}); err != nil {
		t.Errorf("expected a due for an existing member to need no start date, got %v", err)
	}

	newcomer := testutil.InsertUser(t, tx, "")
	if _, err := svc.RecordDue(ctx, newcomer.ID, 2026, testutil.InsertTransaction(t, tx), time.Time{}); !errors.Is(err, memberships.ErrStartDateRequired) {
		t.Errorf("expected ErrStartDateRequired for a first due with no start date, got %v", err)
	}
	if list, err := svc.ListByUser(ctx, newcomer.ID); err != nil || len(list) != 0 {
		t.Errorf("expected the rejected due to start no membership, got %+v (%v)", list, err)
	}
}

package memberships_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
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
	user := testutil.InsertUser(t, tx, "")

	m, err := repo.Create(context.Background(), memberships.CreateInput{
		UserID: user.ID, StartDate: date("2020-01-01"),
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
	user := testutil.InsertUser(t, tx, "")
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
	user := testutil.InsertUser(t, tx, "")
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
	user := testutil.InsertUser(t, tx, "")
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
	user := testutil.InsertUser(t, tx, "")
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

// TestUpdateRejectsShrinkingPeriodPastAnExistingLeave and
// TestUpdateAllowsShrinkingPeriodThatStillContainsExistingLeave cover
// the fix for a previously unchecked invariant: Update let a
// membership's own period narrow past an existing membership_leaves
// row's dates, stranding it outside the very period it's supposed to
// fall entirely within — CreateLeave/UpdateLeave already check this
// from the leave's own side, but nothing checked it from the
// membership's side until now.
func TestUpdateRejectsShrinkingPeriodPastAnExistingLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}

	// Ending the membership on 2020-06-15 would strand the leave's own
	// 2020-06-01..06-30 window half outside it.
	tooEarly := date("2020-06-15")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &tooEarly}); !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}

	// Pushing the start date forward into the leave's own window is
	// rejected the same way, from the other end.
	tooLate := date("2020-06-15")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: tooLate}); !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

func TestUpdateAllowsShrinkingPeriodThatStillContainsExistingLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}

	// Ending well after the leave's own window still contains it.
	stillFits := date("2020-12-31")
	updated, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &stillFits})
	if err != nil {
		t.Fatalf("expected the update to succeed, got %v", err)
	}
	if updated.EndDate == nil || !updated.EndDate.Equal(stillFits) {
		t.Errorf("expected end date %v, got %v", stillFits, updated.EndDate)
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

func TestDeleteMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	if err := repo.Delete(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected the membership to be gone, got %+v", list)
	}
}

func TestDeleteMembershipNotFound(t *testing.T) {
	repo := memberships.NewRepository(testutil.NewTx(t))
	if err := repo.Delete(context.Background(), 999999); !errors.Is(err, memberships.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
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
	user := testutil.InsertUser(t, tx, "")

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

	active := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: active.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create active: %v", err)
	}

	past := testutil.InsertUser(t, tx, "")
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

func TestCreateLeaveWithinPeriod(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	reason := "operación de rodilla"
	l, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")), Reason: &reason,
	})
	if err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if l.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, l.UserID)
	}
	if l.Reason == nil || *l.Reason != reason {
		t.Errorf("expected reason %q, got %v", reason, l.Reason)
	}
}

func TestCreateLeaveRejectsStartBeforeMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	_, err = repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2019-12-01"), EndDate: ptr(date("2020-01-15")),
	})
	if !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

func TestCreateLeaveRejectsEndAfterMembershipEnds(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	closedEnd := date("2020-06-30")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &closedEnd}); err != nil {
		t.Fatalf("close membership: %v", err)
	}

	_, err = repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-07-15")),
	})
	if !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

func TestCreateLeaveAllowedWhenMembershipStillOpen(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	// The membership is still open (no end_date), so a leave far in the
	// future is fine — there's no upper bound to violate yet.
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2030-01-01"), EndDate: ptr(date("2030-02-01")),
	}); err != nil {
		t.Errorf("expected the leave to be accepted, got %v", err)
	}
}

// TestCreateLeaveAllowsOpenEndedLeave covers a leave that's still
// ongoing, with no end_date known yet — the same "open" shape a
// membership period itself already allows.
func TestCreateLeaveAllowsOpenEndedLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	l, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"),
	})
	if err != nil {
		t.Fatalf("expected an open-ended leave to be accepted, got %v", err)
	}
	if l.EndDate != nil {
		t.Errorf("expected a nil end_date, got %v", l.EndDate)
	}
}

// TestCreateLeaveRejectsOpenEndedLeaveWhenMembershipHasClosed is the
// flip side: a leave with no end_date can't be contained within a
// membership period that has itself already closed — there's nothing
// bounding how far into the (nonexistent) future it would stretch.
func TestCreateLeaveRejectsOpenEndedLeaveWhenMembershipHasClosed(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	closedEnd := date("2020-12-31")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &closedEnd}); err != nil {
		t.Fatalf("close membership: %v", err)
	}

	_, err = repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"),
	})
	if !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

// TestUpdateLeaveCanClearEndDateToReopenIt mirrors
// memberships.Update's own "clear end_date to undo an accidental baja"
// story, one level down: correcting a leave to remove its end_date
// works, as long as it's still contained (open-ended) within its own
// still-open membership period.
func TestUpdateLeaveCanClearEndDateToReopenIt(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	l, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	})
	if err != nil {
		t.Fatalf("create leave: %v", err)
	}

	updated, err := repo.UpdateLeave(ctx, l.ID, memberships.UpdateLeaveInput{StartDate: l.StartDate})
	if err != nil {
		t.Fatalf("expected clearing end_date to succeed, got %v", err)
	}
	if updated.EndDate != nil {
		t.Errorf("expected a nil end_date, got %v", updated.EndDate)
	}
}

// TestUpdateRejectsShrinkingPeriodPastAnOpenLeave is
// TestUpdateRejectsShrinkingPeriodPastAnExistingLeave's own open-ended
// counterpart: a membership can't be closed out from under a leave
// that's still ongoing, for the same reason a new open-ended leave
// can't be created against an already-closed one (see
// TestCreateLeaveRejectsOpenEndedLeaveWhenMembershipHasClosed).
func TestUpdateRejectsShrinkingPeriodPastAnOpenLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"),
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}

	closedEnd := date("2020-12-31")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &closedEnd}); !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

// TestCreateLeaveRejectsARangeSpanningTwoOfThePersonsOwnPeriods covers
// the core reason a leave belongs to the person rather than one stored
// membership_id: findContainingMembership must actually check
// containment against each of this person's own periods, not just
// "does this person have any period at all" — a range straddling the
// gap between two real periods isn't contained in either one.
func TestCreateLeaveRejectsARangeSpanningTwoOfThePersonsOwnPeriods(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	firstEnd := date("2015-12-31")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2010-01-01")}); err != nil {
		t.Fatalf("create first period: %v", err)
	}
	first, err := repo.ListByUser(ctx, user.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("list first period: %v %+v", err, first)
	}
	if _, err := repo.Update(ctx, first[0].ID, memberships.UpdateInput{StartDate: first[0].StartDate, EndDate: &firstEnd}); err != nil {
		t.Fatalf("close first period: %v", err)
	}
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create second period: %v", err)
	}

	// 2015-06-01..2020-06-01 straddles the gap between the two periods
	// (2015-12-31 to 2020-01-01) — contained in neither.
	_, err = repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2015-06-01"), EndDate: ptr(date("2020-06-01")),
	})
	if !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

// TestListLeavesByUserSpansEveryOneOfThePersonsOwnPeriods is the whole
// point of keying membership_leaves by user_id rather than
// membership_id: a single call surfaces every leave this person has
// ever taken, across however many separate alta/baja spans they've
// had — not just the ones under whichever period happens to be current.
func TestListLeavesByUserSpansEveryOneOfThePersonsOwnPeriods(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	firstEnd := date("2015-12-31")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2010-01-01")}); err != nil {
		t.Fatalf("create first period: %v", err)
	}
	first, err := repo.ListByUser(ctx, user.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("list first period: %v %+v", err, first)
	}
	older, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2010-06-01"), EndDate: ptr(date("2010-06-30")),
	})
	if err != nil {
		t.Fatalf("create leave in first period: %v", err)
	}
	if _, err := repo.Update(ctx, first[0].ID, memberships.UpdateInput{StartDate: first[0].StartDate, EndDate: &firstEnd}); err != nil {
		t.Fatalf("close first period: %v", err)
	}
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create second period: %v", err)
	}
	newer, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2021-06-01"), EndDate: ptr(date("2021-06-30")),
	})
	if err != nil {
		t.Fatalf("create leave in second period: %v", err)
	}

	list, err := repo.ListLeavesByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list leaves by user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 leaves spanning both periods, got %d: %+v", len(list), list)
	}
	if list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("expected newest-first order [%d, %d], got [%d, %d]", newer.ID, older.ID, list[0].ID, list[1].ID)
	}
}

// TestUpdateShrinkingOnePeriodDoesNotCheckALeaveBelongingToAnotherPeriod
// covers leavesStillFitWithin's own "continue" case: a leave that
// belongs to one of this person's *other* periods (not the one being
// edited) is left alone — only leaves currently within the period
// actually being shrunk are re-checked.
func TestUpdateShrinkingOnePeriodDoesNotCheckALeaveBelongingToAnotherPeriod(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	firstEnd := date("2015-12-31")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2010-01-01")}); err != nil {
		t.Fatalf("create first period: %v", err)
	}
	first, err := repo.ListByUser(ctx, user.ID)
	if err != nil || len(first) != 1 {
		t.Fatalf("list first period: %v %+v", err, first)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2010-06-01"), EndDate: ptr(date("2010-06-30")),
	}); err != nil {
		t.Fatalf("create leave in first period: %v", err)
	}
	if _, err := repo.Update(ctx, first[0].ID, memberships.UpdateInput{StartDate: first[0].StartDate, EndDate: &firstEnd}); err != nil {
		t.Fatalf("close first period: %v", err)
	}
	second, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create second period: %v", err)
	}

	// Shrinking the *second* period to end right after it starts doesn't
	// strand the first period's own leave (2010-06-01..06-30) — that
	// leave was never within the second period to begin with.
	secondEnd := date("2020-01-15")
	if _, err := repo.Update(ctx, second.ID, memberships.UpdateInput{StartDate: second.StartDate, EndDate: &secondEnd}); err != nil {
		t.Errorf("expected shrinking the second period to succeed, got %v", err)
	}
}

func TestCreateLeaveRejectsOverlap(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	}); err != nil {
		t.Fatalf("create first leave: %v", err)
	}

	_, err = repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-15"), EndDate: ptr(date("2020-07-15")),
	})
	if !errors.Is(err, memberships.ErrLeaveOverlap) {
		t.Errorf("expected ErrLeaveOverlap, got %v", err)
	}
}

func TestUpdateLeaveCorrectsDatesAndRevalidatesContainment(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	closedEnd := date("2020-12-31")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &closedEnd}); err != nil {
		t.Fatalf("close membership: %v", err)
	}
	l, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	})
	if err != nil {
		t.Fatalf("create leave: %v", err)
	}

	// A valid correction goes through.
	updated, err := repo.UpdateLeave(ctx, l.ID, memberships.UpdateLeaveInput{
		StartDate: date("2020-07-01"), EndDate: ptr(date("2020-07-31")),
	})
	if err != nil {
		t.Fatalf("update leave: %v", err)
	}
	if !updated.StartDate.Equal(date("2020-07-01")) {
		t.Errorf("expected corrected start date, got %v", updated.StartDate)
	}

	// Pushing it past the membership's end date is rejected, same as at creation.
	_, err = repo.UpdateLeave(ctx, l.ID, memberships.UpdateLeaveInput{
		StartDate: date("2020-12-01"), EndDate: ptr(date("2021-01-15")),
	})
	if !errors.Is(err, memberships.ErrLeaveOutsideMembership) {
		t.Errorf("expected ErrLeaveOutsideMembership, got %v", err)
	}
}

func TestDeleteLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	l, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: user.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	})
	if err != nil {
		t.Fatalf("create leave: %v", err)
	}

	if err := repo.DeleteLeave(ctx, l.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err := repo.ListLeavesByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected no leaves left, got %+v", list)
	}
}

func TestDeleteLeaveNotFound(t *testing.T) {
	repo := memberships.NewRepository(testutil.NewTx(t))
	if err := repo.DeleteLeave(context.Background(), 999999); !errors.Is(err, memberships.ErrLeaveNotFound) {
		t.Errorf("expected ErrLeaveNotFound, got %v", err)
	}
}

func TestListLeavesByUserReturnsEmptySliceNotNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	_, err := repo.Create(ctx, memberships.CreateInput{UserID: user.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	list, err := repo.ListLeavesByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
}

func TestIsOnLeave(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	ctx := context.Background()

	activeNoLeave := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: activeNoLeave.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if onLeave, err := repo.IsOnLeave(ctx, activeNoLeave.ID); err != nil {
		t.Fatalf("is on leave: %v", err)
	} else if onLeave {
		t.Error("expected a member with no leaves to not be on leave")
	}

	currentlyOnLeave := testutil.InsertUser(t, tx, "")
	_, err := repo.Create(ctx, memberships.CreateInput{UserID: currentlyOnLeave.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: currentlyOnLeave.ID, StartDate: date(yesterday), EndDate: ptr(date(tomorrow)),
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if onLeave, err := repo.IsOnLeave(ctx, currentlyOnLeave.ID); err != nil {
		t.Fatalf("is on leave: %v", err)
	} else if !onLeave {
		t.Error("expected a leave window containing today to report on leave")
	}

	pastLeaveOnly := testutil.InsertUser(t, tx, "")
	_, err = repo.Create(ctx, memberships.CreateInput{UserID: pastLeaveOnly.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := repo.CreateLeave(ctx, memberships.CreateLeaveInput{
		UserID: pastLeaveOnly.ID, StartDate: date("2020-06-01"), EndDate: ptr(date("2020-06-30")),
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if onLeave, err := repo.IsOnLeave(ctx, pastLeaveOnly.ID); err != nil {
		t.Fatalf("is on leave: %v", err)
	} else if onLeave {
		t.Error("expected a leave that already ended to not report on leave")
	}
}

func TestWasMemberOn(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	ctx := context.Background()

	currentlyActive := testutil.InsertUser(t, tx, "")
	if _, err := repo.Create(ctx, memberships.CreateInput{UserID: currentlyActive.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if was, err := repo.WasMemberOn(ctx, currentlyActive.ID, time.Now()); err != nil {
		t.Fatalf("WasMemberOn: %v", err)
	} else if !was {
		t.Error("expected a currently open membership to cover today")
	}

	hasLeft := testutil.InsertUser(t, tx, "")
	m, err := repo.Create(ctx, memberships.CreateInput{UserID: hasLeft.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	pastEnd := date("2024-01-01")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end membership: %v", err)
	}
	if was, err := repo.WasMemberOn(ctx, hasLeft.ID, date("2023-06-01")); err != nil {
		t.Fatalf("WasMemberOn: %v", err)
	} else if !was {
		t.Error("expected a date inside the now-closed period to still count")
	}
	if was, err := repo.WasMemberOn(ctx, hasLeft.ID, time.Now()); err != nil {
		t.Fatalf("WasMemberOn: %v", err)
	} else if was {
		t.Error("expected today, after the period closed, to not count")
	}
	if was, err := repo.WasMemberOn(ctx, hasLeft.ID, date("2019-01-01")); err != nil {
		t.Fatalf("WasMemberOn: %v", err)
	} else if was {
		t.Error("expected a date before the period started to not count")
	}
}

func TestHasAnyMembership(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	ctx := context.Background()

	neverMember := testutil.InsertUser(t, tx, "")
	if has, err := repo.HasAnyMembership(ctx, neverMember.ID); err != nil {
		t.Fatalf("has any membership: %v", err)
	} else if has {
		t.Error("expected someone with no membership at all to report false")
	}

	pastMember := testutil.InsertUser(t, tx, "")
	m, err := repo.Create(ctx, memberships.CreateInput{UserID: pastMember.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	pastEnd := date("2021-01-01")
	if _, err := repo.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end membership: %v", err)
	}
	if has, err := repo.HasAnyMembership(ctx, pastMember.ID); err != nil {
		t.Fatalf("has any membership: %v", err)
	} else if !has {
		t.Error("expected a former member (now-closed period) to still report true")
	}
}

func TestCreateDue(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	txID := testutil.InsertTransaction(t, tx)

	d, err := repo.CreateDue(context.Background(), memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: txID,
	})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	if d.UserID != user.ID || d.Year != 2026 || d.TransactionID != txID {
		t.Errorf("expected user id %d, year 2026, transaction id %d, got %+v", user.ID, txID, d)
	}
}

func TestCreateDueRejectsASecondOneForTheSameYear(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.CreateDue(ctx, memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: testutil.InsertTransaction(t, tx),
	}); err != nil {
		t.Fatalf("create first due: %v", err)
	}
	_, err := repo.CreateDue(ctx, memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: testutil.InsertTransaction(t, tx),
	})
	if !errors.Is(err, memberships.ErrDueAlreadyRecorded) {
		t.Errorf("expected ErrDueAlreadyRecorded, got %v", err)
	}
}

func TestCreateDueRejectsAnUnknownTransaction(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	_, err := repo.CreateDue(context.Background(), memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: 999999999,
	})
	if !errors.Is(err, memberships.ErrUnknownTransaction) {
		t.Errorf("expected ErrUnknownTransaction, got %v", err)
	}
}

func TestListDuesForUserOrdersNewestYearFirstAndScopesToTheUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.CreateDue(ctx, memberships.CreateDueInput{
		UserID: other.ID, Year: 2026, TransactionID: testutil.InsertTransaction(t, tx),
	}); err != nil {
		t.Fatalf("create due for other: %v", err)
	}
	older, err := repo.CreateDue(ctx, memberships.CreateDueInput{
		UserID: user.ID, Year: 2024, TransactionID: testutil.InsertTransaction(t, tx),
	})
	if err != nil {
		t.Fatalf("create older due: %v", err)
	}
	newer, err := repo.CreateDue(ctx, memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: testutil.InsertTransaction(t, tx),
	})
	if err != nil {
		t.Fatalf("create newer due: %v", err)
	}

	list, err := repo.ListDuesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list dues for user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 dues for this user, got %d", len(list))
	}
	if list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("expected newest-year-first order [%d, %d], got [%d, %d]", newer.ID, older.ID, list[0].ID, list[1].ID)
	}
}

// PaidAt is when the payment happened — its transaction's occurred_at —
// not when the due was recorded (created_at): an admin can register a
// past year's due long after it was paid, and the history has to show
// the payment's own date.
func TestListDuesForUserReportsThePaymentDateNotTheRecordingDate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	txID := testutil.InsertTransaction(t, tx)
	paid := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `UPDATE transactions SET occurred_at = $1 WHERE id = $2`, paid, txID); err != nil {
		t.Fatalf("backdate the transaction: %v", err)
	}
	if _, err := repo.CreateDue(ctx, memberships.CreateDueInput{UserID: user.ID, Year: 2024, TransactionID: txID}); err != nil {
		t.Fatalf("create due: %v", err)
	}

	list, err := repo.ListDuesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list dues for user: %v", err)
	}
	if len(list) != 1 || list[0].PaidAt == nil || !list[0].PaidAt.Equal(paid) {
		t.Fatalf("expected PaidAt %v, got %+v", paid, list)
	}
	if !list[0].CreatedAt.After(paid) {
		t.Errorf("expected the recording date to be later than the payment's, got created_at %v", list[0].CreatedAt)
	}
}

// CreateDue's own RETURNING has no join, so PaidAt stays nil there.
func TestCreateDueLeavesPaidAtNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	d, err := repo.CreateDue(context.Background(), memberships.CreateDueInput{
		UserID: user.ID, Year: 2026, TransactionID: testutil.InsertTransaction(t, tx),
	})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	if d.PaidAt != nil {
		t.Errorf("expected PaidAt to be unset on a freshly created due, got %v", d.PaidAt)
	}
}

// One payment can cover several socios' cuotas: listing a transaction's
// dues returns all of them, each with its person's login, and none of
// another transaction's.
func TestListDuesForTransactionReturnsOnlyThatTransactionsDues(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)
	first := testutil.InsertUser(t, tx, "")
	second := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	shared := testutil.InsertTransaction(t, tx)
	for _, person := range []testutil.TestUser{first, second} {
		if _, err := repo.CreateDue(ctx, memberships.CreateDueInput{UserID: person.ID, Year: 2026, TransactionID: shared}); err != nil {
			t.Fatalf("create due for %s: %v", person.Login, err)
		}
	}
	if _, err := repo.CreateDue(ctx, memberships.CreateDueInput{UserID: first.ID, Year: 2025, TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
		t.Fatalf("create the other transaction's due: %v", err)
	}

	list, err := repo.ListDuesForTransaction(ctx, shared)
	if err != nil {
		t.Fatalf("ListDuesForTransaction: %v", err)
	}
	logins := map[string]bool{}
	for _, d := range list {
		logins[d.UserLogin] = true
		if d.Year != 2026 || d.TransactionID != shared || d.PaidAt == nil {
			t.Errorf("unexpected due in the listing: %+v", d)
		}
	}
	if len(list) != 2 || !logins[first.Login] || !logins[second.Login] {
		t.Errorf("expected exactly the two dues of the shared transaction, got %+v", list)
	}
}

func TestListDuesForTransactionIsEmptyForOneThatPaidForNone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := memberships.NewRepository(tx)

	for _, id := range []int64{testutil.InsertTransaction(t, tx), 999999999} {
		list, err := repo.ListDuesForTransaction(context.Background(), id)
		if err != nil || len(list) != 0 {
			t.Errorf("transaction %d: expected an empty list, got %+v, %v", id, list, err)
		}
	}
}

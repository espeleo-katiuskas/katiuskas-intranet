package board_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/board"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCreate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	m, err := repo.Create(context.Background(), board.CreateInput{
		UserID: user.ID, Position: board.PositionPresident, StartDate: date("2020-01-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, m.UserID)
	}
	if m.Position != board.PositionPresident {
		t.Errorf("expected position %q, got %q", board.PositionPresident, m.Position)
	}
	if m.EndDate != nil {
		t.Errorf("expected an open term, got end date %v", m.EndDate)
	}
}

func TestCreateRejectsOverlappingTerm(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionSecretary, StartDate: date("2020-01-01"),
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// An open-ended term overlaps any later start date, even under a
	// different position — nobody holds two board terms at once.
	_, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionTreasurer, StartDate: date("2021-06-01"),
	})
	if !errors.Is(err, board.ErrOverlap) {
		t.Errorf("expected ErrOverlap, got %v", err)
	}
}

func TestCreateAllowsNonOverlappingTermsAfterEnding(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	first, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionMemberAtLarge, StartDate: date("2020-01-01"),
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	firstEnd := date("2020-12-31")
	if _, err := repo.Update(ctx, first.ID, board.UpdateInput{
		Position: first.Position, StartDate: first.StartDate, EndDate: &firstEnd,
	}); err != nil {
		t.Fatalf("end: %v", err)
	}

	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionPresident, StartDate: date("2021-01-01"),
	}); err != nil {
		t.Errorf("expected a term starting after the first one ended to be accepted, got %v", err)
	}
}

func TestUpdateCorrectsFieldsAndCanReopenAClosedTerm(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	m, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionVicePresident, StartDate: date("2020-01-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	end := date("2020-06-01")
	updated, err := repo.Update(ctx, m.ID, board.UpdateInput{
		Position: board.PositionPresident, StartDate: m.StartDate, EndDate: &end,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Position != board.PositionPresident {
		t.Errorf("expected position to change to president, got %q", updated.Position)
	}
	if updated.EndDate == nil || !updated.EndDate.Equal(end) {
		t.Errorf("expected end date %v, got %v", end, updated.EndDate)
	}

	reopened, err := repo.Update(ctx, m.ID, board.UpdateInput{
		Position: updated.Position, StartDate: updated.StartDate, EndDate: nil,
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.EndDate != nil {
		t.Errorf("expected clearing the end date to reopen the term, got %v", reopened.EndDate)
	}
}

func TestUpdateRejectsOverlap(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	first, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionSecretary, StartDate: date("2018-01-01"),
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	firstEnd := date("2019-01-01")
	if _, err := repo.Update(ctx, first.ID, board.UpdateInput{
		Position: first.Position, StartDate: first.StartDate, EndDate: &firstEnd,
	}); err != nil {
		t.Fatalf("end first: %v", err)
	}

	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionTreasurer, StartDate: date("2020-01-01"),
	}); err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Reopening the first term (clearing its end date) now overlaps the
	// second, still-open one.
	_, err = repo.Update(ctx, first.ID, board.UpdateInput{
		Position: first.Position, StartDate: first.StartDate, EndDate: nil,
	})
	if !errors.Is(err, board.ErrOverlap) {
		t.Errorf("expected ErrOverlap, got %v", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	repo := board.NewRepository(testutil.NewTx(t))
	_, err := repo.Update(context.Background(), 999999, board.UpdateInput{
		Position: board.PositionMemberAtLarge, StartDate: date("2020-01-01"),
	})
	if !errors.Is(err, board.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	first, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionSecretary, StartDate: date("2018-01-01"),
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	firstEnd := date("2019-01-01")
	if _, err := repo.Update(ctx, first.ID, board.UpdateInput{
		Position: first.Position, StartDate: first.StartDate, EndDate: &firstEnd,
	}); err != nil {
		t.Fatalf("end first: %v", err)
	}
	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: user.ID, Position: board.PositionPresident, StartDate: date("2020-01-01"),
	}); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: other.ID, Position: board.PositionTreasurer, StartDate: date("2020-01-01"),
	}); err != nil {
		t.Fatalf("create other user's term: %v", err)
	}

	list, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 terms for this user, got %d", len(list))
	}
	// Newest first.
	if list[0].Position != board.PositionPresident || list[1].Position != board.PositionSecretary {
		t.Errorf("expected [president secretary] newest first, got %+v", list)
	}
}

func TestListByUserReturnsEmptySliceNotNilWhenUserHasNoHistory(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	list, err := repo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
}

func TestListActiveIncludesNameAndOnlyCurrentlyOpenTerms(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := board.NewRepository(tx)
	current := testutil.InsertUser(t, tx, "")
	past := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.Create(ctx, board.CreateInput{
		UserID: current.ID, Position: board.PositionPresident, StartDate: date("2020-01-01"),
	}); err != nil {
		t.Fatalf("create current: %v", err)
	}
	ended, err := repo.Create(ctx, board.CreateInput{
		UserID: past.ID, Position: board.PositionTreasurer, StartDate: date("2010-01-01"),
	})
	if err != nil {
		t.Fatalf("create past: %v", err)
	}
	pastEnd := date("2011-01-01")
	if _, err := repo.Update(ctx, ended.ID, board.UpdateInput{
		Position: ended.Position, StartDate: ended.StartDate, EndDate: &pastEnd,
	}); err != nil {
		t.Fatalf("end past: %v", err)
	}

	list, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 active board member, got %d: %+v", len(list), list)
	}
	if list[0].UserID != current.ID {
		t.Errorf("expected the current board member, got %+v", list[0])
	}
	if list[0].FirstName == "" || list[0].LastName == "" {
		t.Errorf("expected first/last name to be joined in, got %+v", list[0])
	}
}

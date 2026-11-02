package activities_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCreateAndGetActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	leader := testutil.InsertUser(t, tx, "")

	created, err := repo.Create(context.Background(), activities.CreateInput{
		Title: "Sima del Viento", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic,
		StartDate: date("2026-09-15"), EndDate: date("2026-09-15"), LeaderID: leader.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Cancelled {
		t.Error("expected a new activity not to be cancelled")
	}
	if created.LeaderID != leader.ID {
		t.Errorf("expected leader_id %d, got %d", leader.ID, created.LeaderID)
	}

	found, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if found.Title != "Sima del Viento" {
		t.Errorf("unexpected title: %q", found.Title)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	repo := activities.NewRepository(testutil.NewTx(t))
	if _, err := repo.GetByID(context.Background(), 999999); !errors.Is(err, activities.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	if _, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, activities.CreateInput{Title: "B", Type: activities.TypeSocial, Visibility: activities.VisibilityPublic, StartDate: date("2026-02-01"), EndDate: date("2026-02-01"), LeaderID: leader.ID}); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, 50, 0, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 activities, got %d", len(list))
	}
}

func TestListSortOrder(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	older, err := repo.Create(ctx, activities.CreateInput{Title: "Older", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer, err := repo.Create(ctx, activities.CreateInput{Title: "Newer", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-02-01"), EndDate: date("2026-02-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}

	descending, err := repo.List(ctx, 50, 0, false)
	if err != nil {
		t.Fatalf("list descending: %v", err)
	}
	if descending[0].ID != newer.ID || descending[1].ID != older.ID {
		t.Errorf("expected newest first by default, got [%d, %d]", descending[0].ID, descending[1].ID)
	}

	ascending, err := repo.List(ctx, 50, 0, true)
	if err != nil {
		t.Fatalf("list ascending: %v", err)
	}
	if ascending[0].ID != older.ID || ascending[1].ID != newer.ID {
		t.Errorf("expected oldest first with ascending=true, got [%d, %d]", ascending[0].ID, ascending[1].ID)
	}
}

// day is today plus n days (UTC, as the database's CURRENT_DATE is), as a date without a time.
func day(n int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+n, 0, 0, 0, 0, time.UTC)
}

func TestListActive(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	create := func(title string, start, end time.Time) activities.Activity {
		t.Helper()
		a, err := repo.Create(ctx, activities.CreateInput{Title: title, Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: start, EndDate: end, LeaderID: leader.ID})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return a
	}
	cancel := func(a activities.Activity) {
		t.Helper()
		if err := repo.Cancel(ctx, a.ID); err != nil {
			t.Fatalf("cancel: %v", err)
		}
	}

	upcoming := create("Still to come", day(10), day(11))
	ongoing := create("Going on", day(-1), day(1))
	endsToday := create("Ends today", day(-2), day(0))
	over := create("Over, nothing pending", day(-30), day(-29))
	overWithMaterial := create("Over, material still out", day(-30), day(-29))
	overFullyReturned := create("Over, loan fully returned", day(-30), day(-29))
	cancelledUpcoming := create("Cancelled, still to come", day(10), day(11))
	cancel(cancelledUpcoming)
	cancelledWithMaterial := create("Cancelled, material still out", day(10), day(11))
	cancel(cancelledWithMaterial)

	user := testutil.InsertUser(t, tx, "")
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	var itemID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO material_items (type, description, category_id) VALUES ('general', 'Test item', $1) RETURNING id
	`, categoryID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	// A loan is always 'completed' the moment it's recorded — what makes it "still out" is that
	// nothing has returned it yet, not its own status (see Repository.ListActive).
	loan := func(a activities.Activity) {
		t.Helper()
		if _, err := tx.Exec(ctx, `
			INSERT INTO material_movements (item_id, user_id, type, activity_id, status)
			VALUES ($1, $2, 'loan', $3, 'completed')
		`, itemID, user.ID, a.ID); err != nil {
			t.Fatalf("insert loan: %v", err)
		}
	}
	loan(overWithMaterial)
	loan(cancelledWithMaterial)
	loan(overFullyReturned)
	if _, err := tx.Exec(ctx, `
		INSERT INTO material_movements (item_id, type, quantity, activity_id, status)
		VALUES ($1, 'return', 1, $2, 'completed')
	`, itemID, overFullyReturned.ID); err != nil {
		t.Fatalf("insert return: %v", err)
	}

	list, err := repo.ListActive(ctx, 50, 0, false)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	got := map[int64]bool{}
	for _, a := range list {
		got[a.ID] = true
	}
	for name, a := range map[string]activities.Activity{
		"one still to come": upcoming, "one going on": ongoing, "one that ends today": endsToday,
		"an over one with an unreturned loan":     overWithMaterial,
		"a cancelled one with an unreturned loan": cancelledWithMaterial,
	} {
		if !got[a.ID] {
			t.Errorf("expected %s to be active", name)
		}
	}
	for name, a := range map[string]activities.Activity{
		"an over one with nothing pending": over, "an over one whose loan was fully returned": overFullyReturned,
		"a cancelled one with nothing pending": cancelledUpcoming,
	} {
		if got[a.ID] {
			t.Errorf("expected %s to not be active", name)
		}
	}
}

// A nil slice marshals to JSON null, not []; the frontend calls .map()
// on the response, so an empty result must come back as [] or the page
// crashes trying to render it.
func TestListReturnsEmptySliceNotNilWhenThereAreNoActivities(t *testing.T) {
	repo := activities.NewRepository(testutil.NewTx(t))
	list, err := repo.List(context.Background(), 50, 0, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
	if len(list) != 0 {
		t.Errorf("expected no activities in a fresh transaction, got %+v", list)
	}
}

func TestUpdate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	created, err := repo.Create(ctx, activities.CreateInput{Title: "Old title", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newTitle := "New title"
	cancelled := true
	newType := activities.TypeSocial
	updated, err := repo.Update(ctx, created.ID, activities.UpdateInput{Title: &newTitle, Cancelled: &cancelled, Type: &newType})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected updated title, got %q", updated.Title)
	}
	if !updated.Cancelled {
		t.Error("expected the activity to be cancelled")
	}
	// And a cancelled one can be reactivated.
	notCancelled := false
	if reactivated, err := repo.Update(ctx, created.ID, activities.UpdateInput{Cancelled: &notCancelled}); err != nil || reactivated.Cancelled {
		t.Errorf("expected cancelled=false to reactivate it, got %+v, %v", reactivated, err)
	}
	if updated.Type != newType {
		t.Errorf("expected updated type, got %q", updated.Type)
	}
	if !updated.StartDate.Equal(date("2026-01-01")) {
		t.Errorf("expected untouched fields to survive, got start date %v", updated.StartDate)
	}
}

// LeaderLogin has to come back correct too, not just LeaderID — it's
// resolved from the *new* leader, not left over from the GetByID join
// done before leader_id changed.
func TestUpdateCanReassignLeader(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")
	newLeader := testutil.InsertUser(t, tx, "")

	created, err := repo.Create(ctx, activities.CreateInput{
		Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic,
		StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := repo.Update(ctx, created.ID, activities.UpdateInput{LeaderID: &newLeader.ID})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.LeaderID != newLeader.ID {
		t.Errorf("expected leader_id %d, got %d", newLeader.ID, updated.LeaderID)
	}
	if updated.LeaderLogin != newLeader.Login {
		t.Errorf("expected leader_login %q, got %q", newLeader.Login, updated.LeaderLogin)
	}

	found, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if found.LeaderID != newLeader.ID || found.LeaderLogin != newLeader.Login {
		t.Errorf("expected the reassignment to persist, got %+v", found)
	}
}

func TestCancel(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	created, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Cancel(ctx, created.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	found, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if !found.Cancelled {
		t.Error("expected the activity to be cancelled")
	}
}

func TestCancelNotFound(t *testing.T) {
	repo := activities.NewRepository(testutil.NewTx(t))
	if err := repo.Cancel(context.Background(), 999999); !errors.Is(err, activities.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateWithInitialParticipants(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	leader := testutil.InsertUser(t, tx, "")
	userA := testutil.InsertUser(t, tx, "")
	userB := testutil.InsertUser(t, tx, "")

	activity, err := repo.Create(ctx, activities.CreateInput{
		Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID,
		ParticipantUserIDs: []int64{userA.ID, userB.ID}, CreatedBy: leader.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.ListParticipants(ctx, activity.ID)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 participants signed up at creation, got %d", len(list))
	}
}

func TestCreateRejectsUnknownParticipant(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	leader := testutil.InsertUser(t, tx, "")
	userA := testutil.InsertUser(t, tx, "")

	_, err := repo.Create(ctx, activities.CreateInput{
		Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID,
		ParticipantUserIDs: []int64{userA.ID, 999999},
	})
	if !errors.Is(err, activities.ErrParticipantNotFound) {
		t.Errorf("expected ErrParticipantNotFound, got %v", err)
	}
}

func TestCreateRejectsUnknownParticipantWithoutCreatingActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	if _, err := repo.Create(ctx, activities.CreateInput{
		Title: "Should not persist", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID,
		ParticipantUserIDs: []int64{999999},
	}); !errors.Is(err, activities.ErrParticipantNotFound) {
		t.Fatalf("expected ErrParticipantNotFound, got %v", err)
	}

	list, err := repo.List(ctx, 50, 0, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, a := range list {
		if a.Title == "Should not persist" {
			t.Errorf("expected the whole activity to roll back when a participant is unknown, but found it: %+v", a)
		}
	}
}

func TestListByUserIncludesLedAndParticipatedActivities(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")

	led, err := repo.Create(ctx, activities.CreateInput{
		Title: "Led", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-02-01"), EndDate: date("2026-02-01"), LeaderID: person.ID,
	})
	if err != nil {
		t.Fatalf("create led activity: %v", err)
	}
	joined, err := repo.Create(ctx, activities.CreateInput{
		Title: "Joined", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: other.ID,
	})
	if err != nil {
		t.Fatalf("create joined activity: %v", err)
	}
	if err := repo.AddParticipant(ctx, joined.ID, person.ID, person.ID); err != nil {
		t.Fatalf("add participant: %v", err)
	}
	// Someone else's activity should never show up.
	if _, err := repo.Create(ctx, activities.CreateInput{
		Title: "Unrelated", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-03-01"), EndDate: date("2026-03-01"), LeaderID: other.ID,
	}); err != nil {
		t.Fatalf("create unrelated activity: %v", err)
	}

	list, err := repo.ListByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 activities, got %d: %+v", len(list), list)
	}
	// Most recent first.
	if list[0].ID != led.ID || list[1].ID != joined.ID {
		t.Errorf("expected [%d, %d] newest first, got [%d, %d]", led.ID, joined.ID, list[0].ID, list[1].ID)
	}
}

func TestListByUserReturnsEmptySliceNotNilWhenPersonHasNoHistory(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	person := testutil.InsertUser(t, tx, "")

	list, err := repo.ListByUser(context.Background(), person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if list == nil {
		t.Errorf("expected an empty slice, got nil")
	}
	if len(list) != 0 {
		t.Errorf("expected no activities, got %d", len(list))
	}
}

// year 2026 is seeded by migration 00009_holidays.sql; this only needs
// one more row in a different year to prove filtering actually happens
// rather than just returning everything.
func TestListHolidaysFiltersByYear(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	if _, err := tx.Exec(ctx, `INSERT INTO holidays (date, name) VALUES ('2027-01-01', 'Año Nuevo 2027')`); err != nil {
		t.Fatalf("insert extra holiday: %v", err)
	}

	list2026, err := repo.ListHolidays(ctx, 2026)
	if err != nil {
		t.Fatalf("list 2026: %v", err)
	}
	if len(list2026) == 0 {
		t.Error("expected the seeded 2026 holidays to be listed")
	}
	for _, h := range list2026 {
		if h.Date.Year() != 2026 {
			t.Errorf("expected only 2026 holidays, got %+v", h)
		}
	}

	list2027, err := repo.ListHolidays(ctx, 2027)
	if err != nil {
		t.Fatalf("list 2027: %v", err)
	}
	if len(list2027) != 1 || list2027[0].Name != "Año Nuevo 2027" {
		t.Errorf("expected exactly the inserted 2027 holiday, got %+v", list2027)
	}

	all, err := repo.ListHolidays(ctx, 0)
	if err != nil {
		t.Fatalf("list all years: %v", err)
	}
	if len(all) != len(list2026)+1 {
		t.Errorf("expected year<=0 to return every year, got %d want %d", len(all), len(list2026)+1)
	}
}

func TestParticipants(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	activity, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	userA := testutil.InsertUser(t, tx, "")
	userB := testutil.InsertUser(t, tx, "")

	if err := repo.AddParticipant(ctx, activity.ID, userA.ID, userA.ID); err != nil {
		t.Fatalf("add participant A: %v", err)
	}
	if err := repo.AddParticipant(ctx, activity.ID, userB.ID, userB.ID); err != nil {
		t.Fatalf("add participant B: %v", err)
	}
	// Joining twice should be a harmless no-op (ON CONFLICT DO NOTHING).
	if err := repo.AddParticipant(ctx, activity.ID, userA.ID, userA.ID); err != nil {
		t.Fatalf("re-add participant A: %v", err)
	}

	list, err := repo.ListParticipants(ctx, activity.ID)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(list))
	}

	if err := repo.RemoveParticipant(ctx, activity.ID, userA.ID); err != nil {
		t.Fatalf("remove participant: %v", err)
	}
	list, err = repo.ListParticipants(ctx, activity.ID)
	if err != nil {
		t.Fatalf("list participants after removal: %v", err)
	}
	if len(list) != 1 || list[0].UserID != userB.ID {
		t.Errorf("expected only userB left, got %+v", list)
	}
}

// IsParticipant only ever looks at activity_participants — Create
// doesn't add the organizer there unless their own login is also given
// as one of the initial participant_logins (see Handler.create), so a
// leader with no such row of their own correctly reads as false here;
// callers that need "attended, organizer included" (like
// activityresources's contribution check) also compare against
// Activity.LeaderID themselves.
func TestIsParticipant(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	activity, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	participant := testutil.InsertUser(t, tx, "")
	stranger := testutil.InsertUser(t, tx, "")
	if err := repo.AddParticipant(ctx, activity.ID, participant.ID, participant.ID); err != nil {
		t.Fatalf("add participant: %v", err)
	}

	if is, err := repo.IsParticipant(ctx, activity.ID, leader.ID); err != nil || is {
		t.Errorf("expected the leader (no participant row of their own here) to read as false, got %v, err %v", is, err)
	}
	if is, err := repo.IsParticipant(ctx, activity.ID, participant.ID); err != nil || !is {
		t.Errorf("expected the participant to be found, got %v, err %v", is, err)
	}
	if is, err := repo.IsParticipant(ctx, activity.ID, stranger.ID); err != nil || is {
		t.Errorf("expected the stranger not to be a participant, got %v, err %v", is, err)
	}
}

func TestCreateAndDeleteHoliday(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.CreateHoliday(ctx, date("2026-04-06"), "Lunes de Pascua")
	if err != nil {
		t.Fatalf("create holiday: %v", err)
	}
	if created.ID == 0 {
		t.Errorf("expected a generated id, got %+v", created)
	}

	list, err := repo.ListHolidays(ctx, 2026)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, h := range list {
		if h.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the created holiday to be listed, got %+v", list)
	}

	deleted, err := repo.DeleteHoliday(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete holiday: %v", err)
	}
	if deleted.Name != "Lunes de Pascua" {
		t.Errorf("expected DeleteHoliday to return the deleted row, got %+v", deleted)
	}
	list, err = repo.ListHolidays(ctx, 2026)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, h := range list {
		if h.ID == created.ID {
			t.Errorf("expected the deleted holiday to be gone, still found %+v", h)
		}
	}
}

func TestCreateHolidayRejectsDuplicateDate(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	// 2026-01-01 is already seeded (migration 00009_holidays.sql).
	if _, err := repo.CreateHoliday(ctx, date("2026-01-01"), "Duplicado"); !errors.Is(err, activities.ErrDuplicateHoliday) {
		t.Errorf("expected ErrDuplicateHoliday, got %v", err)
	}
}

func TestDeleteHolidayNotFound(t *testing.T) {
	repo := activities.NewRepository(testutil.NewTx(t))

	if _, err := repo.DeleteHoliday(context.Background(), 999999); !errors.Is(err, activities.ErrHolidayNotFound) {
		t.Errorf("expected ErrHolidayNotFound, got %v", err)
	}
}

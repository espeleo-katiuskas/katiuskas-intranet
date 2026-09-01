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
	if created.Status != activities.StatusPlanned {
		t.Errorf("expected default status %q, got %q", activities.StatusPlanned, created.Status)
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

	list, err := repo.List(ctx, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 activities, got %d", len(list))
	}
}

func TestListActive(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()
	leader := testutil.InsertUser(t, tx, "")

	planned, err := repo.Create(ctx, activities.CreateInput{Title: "Planned", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2026-01-01"), EndDate: date("2026-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create planned: %v", err)
	}

	completedNoMaterial, err := repo.Create(ctx, activities.CreateInput{Title: "Completed, nothing pending", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2020-01-01"), EndDate: date("2020-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create completed: %v", err)
	}
	completed := activities.StatusCompleted
	if _, err := repo.Update(ctx, completedNoMaterial.ID, activities.UpdateInput{Status: &completed}); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	completedWithMaterial, err := repo.Create(ctx, activities.CreateInput{Title: "Completed, material still out", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2020-01-01"), EndDate: date("2020-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create completed with material: %v", err)
	}
	if _, err := repo.Update(ctx, completedWithMaterial.ID, activities.UpdateInput{Status: &completed}); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	var itemID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO inventory_items (item_type, description, category_id) VALUES ('gear', 'Test item', $1) RETURNING id
	`, categoryID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO item_movements (item_id, user_id, type, activity_id, status)
		VALUES ($1, $2, 'loan', $3, 'active')
	`, itemID, user.ID, completedWithMaterial.ID); err != nil {
		t.Fatalf("insert movement: %v", err)
	}

	cancelled, err := repo.Create(ctx, activities.CreateInput{Title: "Cancelled", Type: activities.TypeCaving, Visibility: activities.VisibilityPublic, StartDate: date("2020-01-01"), EndDate: date("2020-01-01"), LeaderID: leader.ID})
	if err != nil {
		t.Fatalf("create cancelled: %v", err)
	}
	if err := repo.Cancel(ctx, cancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	list, err := repo.ListActive(ctx, 50, 0)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	got := map[int64]bool{}
	for _, a := range list {
		got[a.ID] = true
	}
	if !got[planned.ID] {
		t.Error("expected a planned activity to be active")
	}
	if !got[completedWithMaterial.ID] {
		t.Error("expected a completed activity with an unreturned loan to still be active")
	}
	if got[completedNoMaterial.ID] {
		t.Error("expected a completed activity with nothing pending to not be active")
	}
	if got[cancelled.ID] {
		t.Error("expected a cancelled activity to not be active")
	}
}

// A nil slice marshals to JSON null, not []; the frontend calls .map()
// on the response, so an empty result must come back as [] or the page
// crashes trying to render it.
func TestListReturnsEmptySliceNotNilWhenThereAreNoActivities(t *testing.T) {
	repo := activities.NewRepository(testutil.NewTx(t))
	list, err := repo.List(context.Background(), 50, 0)
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
	completed := activities.StatusCompleted
	newType := activities.TypeSocial
	updated, err := repo.Update(ctx, created.ID, activities.UpdateInput{Title: &newTitle, Status: &completed, Type: &newType})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected updated title, got %q", updated.Title)
	}
	if updated.Status != completed {
		t.Errorf("expected updated status, got %q", updated.Status)
	}
	if updated.Type != newType {
		t.Errorf("expected updated type, got %q", updated.Type)
	}
	if !updated.StartDate.Equal(date("2026-01-01")) {
		t.Errorf("expected untouched fields to survive, got start date %v", updated.StartDate)
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
	if found.Status != activities.StatusCancelled {
		t.Errorf("expected status cancelled, got %q", found.Status)
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
		ParticipantUserIDs: []int64{userA.ID, userB.ID},
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

	list, err := repo.List(ctx, 50, 0)
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
	if err := repo.AddParticipant(ctx, joined.ID, person.ID); err != nil {
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

	if err := repo.AddParticipant(ctx, activity.ID, userA.ID); err != nil {
		t.Fatalf("add participant A: %v", err)
	}
	if err := repo.AddParticipant(ctx, activity.ID, userB.ID); err != nil {
		t.Fatalf("add participant B: %v", err)
	}
	// Joining twice should be a harmless no-op (ON CONFLICT DO NOTHING).
	if err := repo.AddParticipant(ctx, activity.ID, userA.ID); err != nil {
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

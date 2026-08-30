package activities_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/activities"
	"katiuskas-intranet/backend/internal/testutil"
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

	created, err := repo.Create(context.Background(), activities.CreateInput{
		Title: "Sima del Viento", Type: activities.TypeCaving,
		StartDate: date("2026-09-15"), Location: "Sierra de Cazorla",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != activities.StatusPlanned {
		t.Errorf("expected default status %q, got %q", activities.StatusPlanned, created.Status)
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

	if _, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, activities.CreateInput{Title: "B", Type: activities.TypeSocial, StartDate: date("2026-02-01"), Location: "Y"}); err != nil {
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

	created, err := repo.Create(ctx, activities.CreateInput{Title: "Old title", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newTitle := "New title"
	completed := activities.StatusCompleted
	updated, err := repo.Update(ctx, created.ID, activities.UpdateInput{Title: &newTitle, Status: &completed})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected updated title, got %q", updated.Title)
	}
	if updated.Status != completed {
		t.Errorf("expected updated status, got %q", updated.Status)
	}
	if updated.Location != "X" {
		t.Errorf("expected untouched fields to survive, got location %q", updated.Location)
	}
}

func TestCancel(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	created, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X"})
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

	userA := testutil.InsertUser(t, tx, "", false)
	userB := testutil.InsertUser(t, tx, "", false)

	activity, err := repo.Create(ctx, activities.CreateInput{
		Title: "A", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X",
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

	userA := testutil.InsertUser(t, tx, "", false)

	_, err := repo.Create(ctx, activities.CreateInput{
		Title: "A", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X",
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

	if _, err := repo.Create(ctx, activities.CreateInput{
		Title: "Should not persist", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X",
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

func TestParticipants(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activities.NewRepository(tx)
	ctx := context.Background()

	activity, err := repo.Create(ctx, activities.CreateInput{Title: "A", Type: activities.TypeCaving, StartDate: date("2026-01-01"), Location: "X"})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	userA := testutil.InsertUser(t, tx, "", false)
	userB := testutil.InsertUser(t, tx, "", false)

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

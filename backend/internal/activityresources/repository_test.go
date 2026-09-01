package activityresources_test

import (
	"context"
	"errors"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func TestCreateTextResourceAppendsAtPositionOne(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	description := "Buen día de cuevas."
	res, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID,
		Title: "Crónica", Description: &description,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Position != 1 {
		t.Errorf("expected the first resource to get position 1, got %d", res.Position)
	}
	if res.Type != activityresources.ResourceText || res.Title != "Crónica" {
		t.Errorf("unexpected resource: %+v", res)
	}
	if res.Description == nil || *res.Description != description {
		t.Errorf("expected description to round-trip, got %+v", res.Description)
	}
}

func TestCreateAppendsAfterCurrentHighestPosition(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	for i := 0; i < 3; i++ {
		description := "entry"
		if _, err := repo.Create(ctx, activityresources.CreateInput{
			ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID,
			Title: "Entry", Description: &description,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	list, err := repo.ListByActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(list))
	}
	for i, res := range list {
		if res.Position != int32(i+1) {
			t.Errorf("expected position %d, got %d", i+1, res.Position)
		}
	}
}

func TestCreateFileResource(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	fileName, contentType := "foto.jpg", "image/jpeg"
	fileSize := int64(12345)
	res, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceFile, AuthorID: author.ID,
		Title: "Foto de la sima", FileName: &fileName, FileSize: &fileSize, ContentType: &contentType,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Description != nil {
		t.Errorf("expected no description on a file resource, got %+v", res.Description)
	}
	if res.FileName == nil || *res.FileName != fileName {
		t.Errorf("expected file_name to round-trip, got %+v", res.FileName)
	}
	if res.FileSize == nil || *res.FileSize != fileSize {
		t.Errorf("expected file_size to round-trip, got %+v", res.FileSize)
	}
	if res.ContentType == nil || *res.ContentType != contentType {
		t.Errorf("expected content_type to round-trip, got %+v", res.ContentType)
	}
}

// The database's own CHECK constraints (migration 00018) are the real
// source of truth for which fields belong to which type — this
// exercises them directly, the same way inventory's tests exercise its
// own type-specific CHECKs.
func TestCreateRejectsMismatchedFields(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	t.Run("text without description", func(t *testing.T) {
		if _, err := repo.Create(ctx, activityresources.CreateInput{
			ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "No description",
		}); !errors.Is(err, activityresources.ErrInvalidFields) {
			t.Errorf("expected ErrInvalidFields, got %v", err)
		}
	})
}

func TestCreateFileWithoutFileFieldsRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	if _, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceFile, AuthorID: author.ID, Title: "No file fields",
	}); !errors.Is(err, activityresources.ErrInvalidFields) {
		t.Errorf("expected ErrInvalidFields, got %v", err)
	}
}

func TestCreateTextWithFileFieldsRejected(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	fileName := "shouldnt-be-here.txt"
	description := "text with a stray file field"
	if _, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "Bad",
		Description: &description, FileName: &fileName,
	}); !errors.Is(err, activityresources.ErrInvalidFields) {
		t.Errorf("expected ErrInvalidFields, got %v", err)
	}
}

func TestGetByIDResolvesAuthorLogin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	description := "d"
	created, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "T", Description: &description,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Create's own RETURNING can't JOIN, so it never sets AuthorLogin —
	// only a query that goes back through resourceSelect (GetByID,
	// ListByActivity) does.
	if created.AuthorLogin != "" {
		t.Errorf("expected Create's own result to leave AuthorLogin unset, got %q", created.AuthorLogin)
	}

	fetched, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.AuthorLogin != author.Login {
		t.Errorf("expected author_login %q, got %q", author.Login, fetched.AuthorLogin)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	if _, err := repo.GetByID(context.Background(), 999999); !errors.Is(err, activityresources.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByActivityReturnsEmptySliceNotNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	activityID := testutil.InsertActivity(t, tx)

	list, err := repo.ListByActivity(context.Background(), activityID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Errorf("expected an empty (non-nil) slice, got %+v", list)
	}
}

func TestUpdateTitleAndDescription(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	description := "original"
	created, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "Original", Description: &description,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newTitle, newDescription := "Updated", "updated description"
	updated, err := repo.Update(ctx, created.ID, activityresources.UpdateInput{Title: &newTitle, Description: &newDescription})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != newTitle || updated.Description == nil || *updated.Description != newDescription {
		t.Errorf("expected updated fields, got %+v", updated)
	}
}

// Update goes through the same CHECK constraints as Create — setting a
// description on a file resource is rejected here too, not just at
// creation time.
func TestUpdateRejectsDescriptionOnFileResource(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	fileName, contentType := "f.pdf", "application/pdf"
	fileSize := int64(1)
	created, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceFile, AuthorID: author.ID, Title: "File",
		FileName: &fileName, FileSize: &fileSize, ContentType: &contentType,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	description := "not allowed"
	if _, err := repo.Update(ctx, created.ID, activityresources.UpdateInput{Description: &description}); !errors.Is(err, activityresources.ErrInvalidFields) {
		t.Errorf("expected ErrInvalidFields, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	description := "d"
	created, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "T", Description: &description,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, activityresources.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	if err := repo.Delete(context.Background(), 999999); !errors.Is(err, activityresources.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReorder(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	var ids []int64
	for i := 0; i < 3; i++ {
		description := "d"
		res, err := repo.Create(ctx, activityresources.CreateInput{
			ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "T", Description: &description,
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids = append(ids, res.ID)
	}
	// ids is [first, second, third] at positions [1,2,3] — reverse it.
	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := repo.Reorder(ctx, activityID, reversed); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	list, err := repo.ListByActivity(ctx, activityID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 || list[0].ID != ids[2] || list[1].ID != ids[1] || list[2].ID != ids[0] {
		t.Fatalf("expected reversed order, got %+v", list)
	}
	for i, res := range list {
		if res.Position != int32(i+1) {
			t.Errorf("expected dense positions starting at 1, got %+v", list)
		}
	}
}

func TestReorderRejectsMissingID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	ctx := context.Background()
	activityID := testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")

	description := "d"
	res, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "T", Description: &description,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := repo.Create(ctx, activityresources.CreateInput{
		ActivityID: activityID, Type: activityresources.ResourceText, AuthorID: author.ID, Title: "T2", Description: &description,
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Only one of the two current ids — a stale/incomplete list.
	if err := repo.Reorder(ctx, activityID, []int64{res.ID}); !errors.Is(err, activityresources.ErrReorderMismatch) {
		t.Errorf("expected ErrReorderMismatch for a partial list, got %v", err)
	}
	// A duplicate id instead of the real second one.
	if err := repo.Reorder(ctx, activityID, []int64{res.ID, res.ID}); !errors.Is(err, activityresources.ErrReorderMismatch) {
		t.Errorf("expected ErrReorderMismatch for a duplicate id, got %v", err)
	}
	// An id from a different activity entirely.
	otherActivityID := testutil.InsertActivity(t, tx)
	if err := repo.Reorder(ctx, otherActivityID, []int64{res.ID, second.ID}); !errors.Is(err, activityresources.ErrReorderMismatch) {
		t.Errorf("expected ErrReorderMismatch for ids belonging to a different activity, got %v", err)
	}
}

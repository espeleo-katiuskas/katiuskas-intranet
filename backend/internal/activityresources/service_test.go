package activityresources_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// newTestServiceForActivity wires a Service against a real (isolated,
// rolled-back) Postgres transaction for metadata and a t.TempDir() for
// file bytes — the same split Service itself makes between Repository
// and Storage.
func newTestServiceForActivity(t *testing.T) (svc *activityresources.Service, storage *activityresources.Storage, activityID, authorID int64) {
	t.Helper()
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	activityID = testutil.InsertActivity(t, tx)
	author := testutil.InsertUser(t, tx, "")
	return activityresources.NewService(repo, storage), storage, activityID, author.ID
}

func TestServiceCreateFileSavesBothRowAndBytes(t *testing.T) {
	service, _, activityID, authorID := newTestServiceForActivity(t)
	ctx := context.Background()

	res, err := service.CreateFile(ctx, activityID, authorID, "Foto", "foto.jpg", "image/jpeg", 5, bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	_, f, err := service.OpenFile(ctx, res.ID)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("expected file content to round-trip, got %q", content)
	}
}

func TestServiceOpenFileRejectsTextResource(t *testing.T) {
	service, _, activityID, authorID := newTestServiceForActivity(t)
	ctx := context.Background()
	res, err := service.CreateText(ctx, activityID, authorID, "Crónica", "texto")
	if err != nil {
		t.Fatalf("create text: %v", err)
	}
	if _, _, err := service.OpenFile(ctx, res.ID); !errors.Is(err, activityresources.ErrNotAFile) {
		t.Errorf("expected ErrNotAFile, got %v", err)
	}
}

func TestServiceUpdateRejectsDescriptionOnFileResource(t *testing.T) {
	service, _, activityID, authorID := newTestServiceForActivity(t)
	ctx := context.Background()
	res, err := service.CreateFile(ctx, activityID, authorID, "Foto", "foto.jpg", "image/jpeg", 1, bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	description := "not allowed"
	if _, err := service.Update(ctx, res.ID, activityresources.UpdateInput{Description: &description}); !errors.Is(err, activityresources.ErrDescriptionNotAllowed) {
		t.Errorf("expected ErrDescriptionNotAllowed, got %v", err)
	}
}

// Delete removes the metadata row and, best-effort, the file that went
// with it — checked here against the same Storage instance Service
// itself used, not just by re-fetching the (now-gone) row.
func TestServiceDeleteRemovesBothRowAndFile(t *testing.T) {
	service, storage, activityID, authorID := newTestServiceForActivity(t)
	ctx := context.Background()
	res, err := service.CreateFile(ctx, activityID, authorID, "Foto", "foto.jpg", "image/jpeg", 1, bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := service.Delete(ctx, res.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := service.OpenFile(ctx, res.ID); !errors.Is(err, activityresources.ErrNotFound) {
		t.Errorf("expected ErrNotFound (the metadata row is gone), got %v", err)
	}
	if _, err := storage.Open(activityID, res.ID); err == nil {
		t.Error("expected the file itself to have been removed from disk too")
	}
}

func TestServiceDeleteTextResourceLeavesNothingOnDisk(t *testing.T) {
	service, storage, activityID, authorID := newTestServiceForActivity(t)
	ctx := context.Background()
	res, err := service.CreateText(ctx, activityID, authorID, "Crónica", "texto")
	if err != nil {
		t.Fatalf("create text: %v", err)
	}
	if err := service.Delete(ctx, res.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// A text resource never had a file to begin with — deleting it
	// shouldn't try to remove one (Service.Delete only does that for
	// ResourceFile), and definitely shouldn't error out over it.
	if _, err := storage.Open(activityID, res.ID); err == nil {
		t.Error("expected no file to exist for a text resource")
	}
}

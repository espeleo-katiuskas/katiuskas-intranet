package activityresources_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
)

func TestStorageSaveAndOpen(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.Save(1, 42, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatalf("save: %v", err)
	}
	f, err := storage.Open(1, 42)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("expected %q, got %q", "hello", content)
	}
}

// Two different activities' resources never collide even if they
// happen to share the same resource id numerically — each activity
// gets its own subdirectory.
func TestStorageScopesByActivity(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.Save(1, 1, bytes.NewReader([]byte("activity one"))); err != nil {
		t.Fatalf("save for activity 1: %v", err)
	}
	if err := storage.Save(2, 1, bytes.NewReader([]byte("activity two"))); err != nil {
		t.Fatalf("save for activity 2: %v", err)
	}

	f1, err := storage.Open(1, 1)
	if err != nil {
		t.Fatalf("open activity 1: %v", err)
	}
	content1, _ := io.ReadAll(f1)
	f1.Close()
	if string(content1) != "activity one" {
		t.Errorf("expected activity 1's own content, got %q", content1)
	}

	f2, err := storage.Open(2, 1)
	if err != nil {
		t.Fatalf("open activity 2: %v", err)
	}
	content2, _ := io.ReadAll(f2)
	f2.Close()
	if string(content2) != "activity two" {
		t.Errorf("expected activity 2's own content, got %q", content2)
	}
}

func TestStorageOpenNotFound(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if _, err := storage.Open(1, 999); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected a not-exist error, got %v", err)
	}
}

func TestStorageDelete(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.Save(1, 1, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := storage.Delete(1, 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := storage.Open(1, 1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected a not-exist error after delete, got %v", err)
	}
}

// failingReader returns n bytes successfully, then a fixed error on
// every read after that — enough to make io.Copy fail partway through,
// simulating an upload that fails mid-transfer (the client
// disconnecting, a read error off the request body).
type failingReader struct {
	remaining int
	err       error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, r.err
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	r.remaining -= n
	return n, nil
}

// TestStorageSaveRemovesPartialFileOnCopyFailure covers the fix for a
// previously leaked partial file: Save's own io.Copy failing partway
// used to leave whatever had already been written behind — Service.
// CreateFile's cleanup on a Save failure only ever deletes the metadata
// row it just created, never reaches into storage, so nothing else was
// going to catch this either.
func TestStorageSaveRemovesPartialFileOnCopyFailure(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	readErr := errors.New("connection reset")
	err = storage.Save(1, 1, &failingReader{remaining: 4, err: readErr})
	if !errors.Is(err, readErr) {
		t.Fatalf("expected the read error to surface, got %v", err)
	}
	if _, err := storage.Open(1, 1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no partial file left behind, got err=%v", err)
	}
}

// Saving to a resource id that already has a file must fail and leave
// that file untouched — including when it's a hard link, whose inode is
// shared with another directory tree (production's files, in the
// testing environment): truncating it would change the other tree's file
// too.
func TestStorageSaveRefusesToOverwriteAnExistingFile(t *testing.T) {
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if err := storage.Save(1, 1, bytes.NewReader([]byte("original"))); err != nil {
		t.Fatalf("first save: %v", err)
	}

	err = storage.Save(1, 1, bytes.NewReader([]byte("replacement")))
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist on a second save to the same ids, got %v", err)
	}

	f, err := storage.Open(1, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	if string(content) != "original" {
		t.Errorf("expected the original content to survive, got %q", content)
	}
}

func TestStorageSaveDoesNotTouchAHardLinkedFile(t *testing.T) {
	prodRoot, testingRoot := t.TempDir(), t.TempDir()
	prod, err := activityresources.NewStorage(prodRoot)
	if err != nil {
		t.Fatalf("new prod storage: %v", err)
	}
	if err := prod.Save(7, 3, bytes.NewReader([]byte("production file"))); err != nil {
		t.Fatalf("prod save: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(testingRoot, "7"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Link(filepath.Join(prodRoot, "7", "3"), filepath.Join(testingRoot, "7", "3")); err != nil {
		t.Skipf("hard links not supported here: %v", err)
	}
	linked, err := activityresources.NewStorage(testingRoot)
	if err != nil {
		t.Fatalf("new testing storage: %v", err)
	}

	if err := linked.Save(7, 3, bytes.NewReader([]byte("x"))); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}

	f, err := prod.Open(7, 3)
	if err != nil {
		t.Fatalf("open prod file: %v", err)
	}
	defer f.Close()
	content, _ := io.ReadAll(f)
	if string(content) != "production file" {
		t.Errorf("production's file was changed through the hard link: %q", content)
	}

	if err := linked.Delete(7, 3); err != nil {
		t.Fatalf("delete from the linked tree: %v", err)
	}
	if _, err := prod.Open(7, 3); err != nil {
		t.Errorf("deleting the link in one tree must leave the other tree's file in place: %v", err)
	}
}

func TestNewStorageCreatesTheRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "activity-resources")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to exist yet", root)
	}
	if _, err := activityresources.NewStorage(root); err != nil {
		t.Fatalf("new storage: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %s to have been created as a directory, err=%v", root, err)
	}
}

package transactionfiles_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/transactionfiles"
)

func TestStorageSaveOpenDelete(t *testing.T) {
	root := t.TempDir()
	s, err := transactionfiles.NewStorage(filepath.Join(root, "new", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(7, 3, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(7, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	// Never rewritten: another tree may share the file by hard link.
	if err := s.Save(7, 3, strings.NewReader("changed")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second save: %v", err)
	}
	if err := s.Delete(7, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(7, 3); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open after delete: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disconnected") }

func TestStorageSaveLeavesNothingWhenTheReadFails(t *testing.T) {
	s, _ := transactionfiles.NewStorage(t.TempDir())
	if err := s.Save(1, 1, failingReader{}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := s.Open(1, 1); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a partial file was left: %v", err)
	}
}

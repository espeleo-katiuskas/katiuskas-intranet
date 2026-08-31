package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/library"
)

// newTestService sets up a small tree under a fresh t.TempDir():
//
//	root/
//	  Estatutos.pdf
//	  Actas/
//	    2024-acta.md
//	  .git/            (hidden — never listed)
//	    config
func newTestService(t *testing.T) *library.Service {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "Estatutos.pdf"), "pdf bytes")
	if err := os.MkdirAll(filepath.Join(root, "Actas"), 0o755); err != nil {
		t.Fatalf("mkdir Actas: %v", err)
	}
	write(t, filepath.Join(root, "Actas", "2024-acta.md"), "# Acta 2024")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	write(t, filepath.Join(root, ".git", "config"), "hidden")

	svc, err := library.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewServiceCreatesTheRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "library")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to exist yet", root)
	}
	if _, err := library.NewService(root); err != nil {
		t.Fatalf("new service: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %s to have been created as a directory, err=%v", root, err)
	}
}

func TestNewServiceRejectsARootThatIsAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-dir")
	write(t, root, "just a file")
	if _, err := library.NewService(root); err == nil {
		t.Error("expected an error when root is a file, not a directory")
	}
}

func TestListDirectoryListsRootByDefault(t *testing.T) {
	svc := newTestService(t)

	listing, err := svc.ListDirectory("")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if listing.Path != "" {
		t.Errorf("expected empty path for the root, got %q", listing.Path)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("expected 2 visible entries (dotfiles hidden), got %d: %+v", len(listing.Entries), listing.Entries)
	}
	// Directories first, then alphabetical.
	if listing.Entries[0].Name != "Actas" || listing.Entries[0].Type != library.EntryDir {
		t.Errorf("expected Actas dir first, got %+v", listing.Entries[0])
	}
	if listing.Entries[1].Name != "Estatutos.pdf" || listing.Entries[1].Type != library.EntryFile {
		t.Errorf("expected Estatutos.pdf second, got %+v", listing.Entries[1])
	}
	if listing.Entries[1].Size == 0 {
		t.Error("expected a non-zero size for a file entry")
	}
}

func TestListDirectoryListsASubdirectory(t *testing.T) {
	svc := newTestService(t)

	listing, err := svc.ListDirectory("Actas")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if listing.Path != "Actas" {
		t.Errorf("expected path %q, got %q", "Actas", listing.Path)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "2024-acta.md" {
		t.Fatalf("expected just 2024-acta.md, got %+v", listing.Entries)
	}
}

func TestListDirectoryHidesDotfiles(t *testing.T) {
	svc := newTestService(t)
	listing, err := svc.ListDirectory("")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	for _, e := range listing.Entries {
		if e.Name == ".git" {
			t.Errorf("expected .git to be hidden, got %+v", listing.Entries)
		}
	}
}

// cleanRelPath anchors every path under a synthetic root before
// cleaning, so a ".." can never actually reach above the real root — it
// always resolves to somewhere inside it (root itself, or some
// subpath), the same trick net/http's own file server uses. None of
// these ever produce ErrInvalidPath; whether they 404 depends only on
// whether the resulting in-root path happens to exist.
func TestListDirectoryNeutralizesPathEscapesInsteadOfLeakingOutsideRoot(t *testing.T) {
	svc := newTestService(t)

	// "../" and ".." both collapse to the root itself, which exists.
	for _, escape := range []string{"../", ".."} {
		t.Run(escape, func(t *testing.T) {
			listing, err := svc.ListDirectory(escape)
			if err != nil {
				t.Fatalf("expected the root's own listing, got error %v", err)
			}
			if listing.Path != "" {
				t.Errorf("expected the root path, got %q", listing.Path)
			}
		})
	}

	// "../../etc" collapses to "/etc" (i.e. "etc" under root), which
	// doesn't exist in this fixture — a plain 404, never a path outside
	// root, and never ErrInvalidPath.
	for _, escape := range []string{"../../etc", "a/../../b"} {
		t.Run(escape, func(t *testing.T) {
			_, err := svc.ListDirectory(escape)
			if !errors.Is(err, library.ErrNotFound) {
				t.Errorf("expected ErrNotFound (a neutralized escape resolving to a nonexistent in-root path), got %v", err)
			}
		})
	}
}

func TestListDirectoryNotFound(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.ListDirectory("nope"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListDirectoryRejectsAFilePath(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.ListDirectory("Estatutos.pdf"); !errors.Is(err, library.ErrNotDirectory) {
		t.Errorf("expected ErrNotDirectory, got %v", err)
	}
}

func TestOpenFile(t *testing.T) {
	svc := newTestService(t)
	f, err := svc.OpenFile("Actas/2024-acta.md")
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Content.Close()

	if f.Name != "2024-acta.md" {
		t.Errorf("expected name 2024-acta.md, got %q", f.Name)
	}
	buf := make([]byte, f.Size)
	if _, err := f.Content.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "# Acta 2024" {
		t.Errorf("expected file content, got %q", buf)
	}
}

func TestOpenFileNotFound(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.OpenFile("nope.pdf"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestOpenFileRejectsADirectoryPath(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.OpenFile("Actas"); !errors.Is(err, library.ErrIsDirectory) {
		t.Errorf("expected ErrIsDirectory, got %v", err)
	}
}

// A symlink planted inside the tree that points outside it is the one
// way cleanRelPath's own anchoring trick doesn't already neutralize —
// resolve() must catch it by checking where the symlink actually leads.
func TestSymlinkEscapingRootIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "top secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	svc, err := library.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := svc.OpenFile("link.txt"); !errors.Is(err, library.ErrInvalidPath) {
		t.Errorf("expected ErrInvalidPath for a symlink escaping root, got %v", err)
	}
}

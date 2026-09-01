package userfiles_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/userfiles"
)

// newTestService sets up a small tree under a fresh t.TempDir():
//
//	root/
//	  juan.perez/
//	    DNI.pdf
//	    Licencias/
//	      2024-federativa.md
//	    .git/          (hidden — never listed)
//	      config
//
// maria.gomez has no subdirectory at all — the common case for someone
// nobody's dropped a file in for yet.
func newTestService(t *testing.T) *userfiles.Service {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "juan.perez", "DNI.pdf"), "pdf bytes")
	if err := os.MkdirAll(filepath.Join(root, "juan.perez", "Licencias"), 0o755); err != nil {
		t.Fatalf("mkdir Licencias: %v", err)
	}
	write(t, filepath.Join(root, "juan.perez", "Licencias", "2024-federativa.md"), "# Federativa 2024")
	if err := os.MkdirAll(filepath.Join(root, "juan.perez", ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	write(t, filepath.Join(root, "juan.perez", ".git", "config"), "hidden")

	svc, err := userfiles.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewServiceCreatesTheRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "user-files")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to exist yet", root)
	}
	if _, err := userfiles.NewService(root); err != nil {
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
	if _, err := userfiles.NewService(root); err == nil {
		t.Error("expected an error when root is a file, not a directory")
	}
}

func TestListDirectoryListsAPersonsOwnRootByDefault(t *testing.T) {
	svc := newTestService(t)

	listing, err := svc.ListDirectory("juan.perez", "")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if listing.Path != "" {
		t.Errorf("expected empty path for the person's own root, got %q", listing.Path)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("expected 2 visible entries (dotfiles hidden), got %d: %+v", len(listing.Entries), listing.Entries)
	}
	// Directories first, then alphabetical.
	if listing.Entries[0].Name != "Licencias" || listing.Entries[0].Type != userfiles.EntryDir {
		t.Errorf("expected Licencias dir first, got %+v", listing.Entries[0])
	}
	if listing.Entries[1].Name != "DNI.pdf" || listing.Entries[1].Type != userfiles.EntryFile {
		t.Errorf("expected DNI.pdf second, got %+v", listing.Entries[1])
	}
	if listing.Entries[1].Size == 0 {
		t.Error("expected a non-zero size for a file entry")
	}
}

func TestListDirectoryListsASubdirectory(t *testing.T) {
	svc := newTestService(t)

	listing, err := svc.ListDirectory("juan.perez", "Licencias")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if listing.Path != "Licencias" {
		t.Errorf("expected path %q, got %q", "Licencias", listing.Path)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "2024-federativa.md" {
		t.Fatalf("expected just 2024-federativa.md, got %+v", listing.Entries)
	}
}

func TestListDirectoryHidesDotfiles(t *testing.T) {
	svc := newTestService(t)
	listing, err := svc.ListDirectory("juan.perez", "")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	for _, e := range listing.Entries {
		if e.Name == ".git" {
			t.Errorf("expected .git to be hidden, got %+v", listing.Entries)
		}
	}
}

// The whole point of scoping every path under login first: two people
// never see each other's files, even by name collision.
func TestListDirectoryScopesEachPersonToTheirOwnSubtree(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.ListDirectory("maria.gomez", "Licencias"); !errors.Is(err, userfiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a subdirectory that only exists under a different login, got %v", err)
	}
}

// A person nobody's dropped a file in for yet gets an empty listing at
// their own root, not a 404 — the common case, since most members won't
// have a subdirectory of their own for a long time, if ever.
func TestListDirectoryEmptyForAPersonWithNoSubtreeYet(t *testing.T) {
	svc := newTestService(t)

	listing, err := svc.ListDirectory("maria.gomez", "")
	if err != nil {
		t.Fatalf("expected an empty listing, not an error, got %v", err)
	}
	if listing.Path != "" || len(listing.Entries) != 0 {
		t.Errorf("expected an empty listing, got %+v", listing)
	}
}

// A *specific* nonexistent subdirectory under a person with no subtree
// at all still 404s normally — only their own root is special-cased.
func TestListDirectoryNotFoundForASpecificSubdirectoryOfAPersonWithNoSubtreeYet(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.ListDirectory("maria.gomez", "Licencias"); !errors.Is(err, userfiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// cleanRelPath anchors every path under a synthetic per-login root
// before cleaning, so a ".." can never actually reach above that
// person's own subtree — it always resolves to somewhere inside it.
func TestListDirectoryNeutralizesPathEscapesInsteadOfLeakingOutsideThePersonsSubtree(t *testing.T) {
	svc := newTestService(t)

	// "../" and ".." both collapse to the person's own root, which
	// exists.
	for _, escape := range []string{"../", ".."} {
		t.Run(escape, func(t *testing.T) {
			listing, err := svc.ListDirectory("juan.perez", escape)
			if err != nil {
				t.Fatalf("expected the person's own root listing, got error %v", err)
			}
			if listing.Path != "" {
				t.Errorf("expected the root path, got %q", listing.Path)
			}
		})
	}

	// "../../etc" collapses to "juan.perez/etc" under root, which
	// doesn't exist in this fixture — a plain 404, never a path outside
	// the person's own subtree, and never ErrInvalidPath.
	for _, escape := range []string{"../../etc", "a/../../b"} {
		t.Run(escape, func(t *testing.T) {
			_, err := svc.ListDirectory("juan.perez", escape)
			if !errors.Is(err, userfiles.ErrNotFound) {
				t.Errorf("expected ErrNotFound (a neutralized escape resolving to a nonexistent in-subtree path), got %v", err)
			}
		})
	}

	// A login itself that tries to escape (never a real login by the
	// time Handler calls in, but resolve() is defense in depth) is
	// neutralized the same way, landing inside root rather than outside
	// it.
	if _, err := svc.ListDirectory("../../etc", ""); err != nil && !errors.Is(err, userfiles.ErrNotFound) {
		t.Errorf("expected either the root listing or ErrNotFound, never anything else, got %v", err)
	}
}

func TestListDirectoryRejectsAFilePath(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.ListDirectory("juan.perez", "DNI.pdf"); !errors.Is(err, userfiles.ErrNotDirectory) {
		t.Errorf("expected ErrNotDirectory, got %v", err)
	}
}

func TestOpenFile(t *testing.T) {
	svc := newTestService(t)
	f, err := svc.OpenFile("juan.perez", "Licencias/2024-federativa.md")
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer f.Content.Close()

	if f.Name != "2024-federativa.md" {
		t.Errorf("expected name 2024-federativa.md, got %q", f.Name)
	}
	buf := make([]byte, f.Size)
	if _, err := f.Content.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "# Federativa 2024" {
		t.Errorf("expected file content, got %q", buf)
	}
}

func TestOpenFileNotFound(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.OpenFile("juan.perez", "nope.pdf"); !errors.Is(err, userfiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestOpenFileNotFoundForAPersonWithNoSubtreeYet(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.OpenFile("maria.gomez", "nope.pdf"); !errors.Is(err, userfiles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestOpenFileRejectsADirectoryPath(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.OpenFile("juan.perez", "Licencias"); !errors.Is(err, userfiles.ErrIsDirectory) {
		t.Errorf("expected ErrIsDirectory, got %v", err)
	}
}

// A symlink planted inside a person's own subtree that points outside
// it is the one way cleanRelPath's own anchoring trick doesn't already
// neutralize — resolve() must catch it by checking where the symlink
// actually leads.
func TestSymlinkEscapingRootIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "top secret")
	if err := os.MkdirAll(filepath.Join(root, "juan.perez"), 0o755); err != nil {
		t.Fatalf("mkdir juan.perez: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "juan.perez", "link.txt")); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	svc, err := userfiles.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := svc.OpenFile("juan.perez", "link.txt"); !errors.Is(err, userfiles.ErrInvalidPath) {
		t.Errorf("expected ErrInvalidPath for a symlink escaping root, got %v", err)
	}
}

package userfiles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	// ErrNotFound means the given path doesn't exist under this person's
	// subtree.
	ErrNotFound = errors.New("not found")
	// ErrInvalidPath means the given path tries to escape this person's
	// subtree (a "..", an absolute path, or a symlink pointing outside
	// it) — never a legitimate request, so it's never distinguished from
	// ErrNotFound in what a caller is told (see Handler), only in what
	// gets logged.
	ErrInvalidPath = errors.New("path escapes this person's files")
	// ErrIsDirectory means a file was expected but the path names a
	// directory.
	ErrIsDirectory = errors.New("path is a directory, not a file")
	// ErrNotDirectory means a directory was expected but the path names
	// a file.
	ErrNotDirectory = errors.New("path is a file, not a directory")
)

type Service struct {
	// root is absolute and symlink-resolved once at startup, so every
	// later comparison in resolve is between two already-clean absolute
	// paths.
	root string
}

// NewService resolves root once at startup, creating it if it doesn't
// exist yet — a fresh deployment shouldn't have to pre-create an empty
// directory by hand before the API will even boot. A root that exists
// but isn't a directory still fails fast here rather than on every
// request. Individual login subdirectories are *not* created here —
// most people will never have one, and nothing should conjure up an
// empty directory for every socio just so ListDirectory has something
// to stat (see its own ErrNotFound handling instead).
func NewService(root string) (*Service, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create user files root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Service{root: resolved}, nil
}

// cleanRelPath normalizes a caller-given path into a slash-separated,
// root-relative form with no leading slash and no ".." segments — "" for
// the root itself. Anchoring at "/" before cleaning is what makes ".."
// harmless: path.Clean can't walk it above that synthetic root, the same
// trick net/http's own file server uses.
func cleanRelPath(relPath string) string {
	return strings.TrimPrefix(path.Clean("/"+relPath), "/")
}

// resolve maps a login plus a caller-given path (both untrusted: a URL
// path segment and a query parameter) to an absolute filesystem path
// guaranteed to be inside root, following symlinks and rejecting any
// that lead back outside it. login is joined in as if it were simply
// the first path segment and run through the exact same cleaning and
// confinement as relPath — a login can never legitimately contain a "/"
// or "..", but by the time this is called it's already a real login
// resolved from the users table (see Handler.resolveUserLogin), so this
// is defense in depth rather than the primary safeguard.
func (s *Service) resolve(login, relPath string) (string, error) {
	full := filepath.Join(s.root, cleanRelPath(path.Join(login, relPath)))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return resolved, nil
}

// ListDirectory lists relPath's immediate contents within login's own
// subtree, directories first then files, both alphabetical
// (case-insensitive) — dotfiles are never listed. A person with no
// subdirectory of their own yet (the common case: nobody's dropped a
// file in for them) sees an empty listing at their own root rather than
// a 404 — a request for some *specific* nonexistent subdirectory still
// 404s normally.
func (s *Service) ListDirectory(login, relPath string) (Listing, error) {
	full, err := s.resolve(login, relPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) && relPath == "" {
			return Listing{Path: "", Entries: []Entry{}}, nil
		}
		return Listing{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return Listing{}, ErrNotFound
		}
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDirectory
	}

	dirEntries, err := os.ReadDir(full)
	if err != nil {
		return Listing{}, err
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		e := Entry{Name: de.Name(), ModifiedAt: fi.ModTime()}
		if de.IsDir() {
			e.Type = EntryDir
		} else {
			e.Type = EntryFile
			e.Size = fi.Size()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Type == EntryDir) != (entries[j].Type == EntryDir) {
			return entries[i].Type == EntryDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return Listing{Path: cleanRelPath(relPath), Entries: entries}, nil
}

// File is an open document ready to be streamed to a response, along
// with what the caller needs to set the right headers. Close must be
// called once the caller is done with Content.
type File struct {
	Content    io.ReadSeekCloser
	Name       string
	Size       int64
	ModifiedAt time.Time
}

// OpenFile opens relPath, within login's own subtree, for reading. The
// caller is responsible for closing File.Content.
func (s *Service) OpenFile(login, relPath string) (File, error) {
	full, err := s.resolve(login, relPath)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	if info.IsDir() {
		return File{}, ErrIsDirectory
	}
	f, err := os.Open(full)
	if err != nil {
		return File{}, err
	}
	return File{Content: f, Name: info.Name(), Size: info.Size(), ModifiedAt: info.ModTime()}, nil
}

package library

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	// ErrNotFound means the given path doesn't exist under the library
	// root.
	ErrNotFound = errors.New("not found")
	// ErrInvalidPath means the given path tries to escape the library
	// root (a "..", an absolute path, or a symlink pointing outside it)
	// — never a legitimate request, so it's never distinguished from
	// ErrNotFound in what a caller is told (see Handler), only in what
	// gets logged.
	ErrInvalidPath = errors.New("path escapes the document library")
	// ErrIsDirectory means a file was expected but the path names a
	// directory.
	ErrIsDirectory = errors.New("path is a directory, not a file")
	// ErrNotDirectory means a directory was expected but the path names
	// a file.
	ErrNotDirectory = errors.New("path is a file, not a directory")
)

func init() {
	// Go's mime package falls back to a small built-in table when the
	// host has no /etc/mime.types (true of the alpine-based API image),
	// and that table doesn't include every extension a club's documents
	// realistically use. Registered once, globally, like any mime
	// package caller is expected to do.
	for ext, typ := range map[string]string{
		".md":   "text/markdown; charset=utf-8",
		".txt":  "text/plain; charset=utf-8",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".odt":  "application/vnd.oasis.opendocument.text",
		".ods":  "application/vnd.oasis.opendocument.spreadsheet",
		".csv":  "text/csv; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

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
// request.
func NewService(root string) (*Service, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create library root: %w", err)
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

// resolve maps a caller-given path (untrusted: a URL query parameter) to
// an absolute filesystem path guaranteed to be inside root, following
// symlinks and rejecting any that lead back outside it — a symlink
// planted inside the tree is the one way cleanRelPath's own safety
// doesn't already cover.
func (s *Service) resolve(relPath string) (string, error) {
	full := filepath.Join(s.root, cleanRelPath(relPath))
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

// ListDirectory lists relPath's immediate contents, directories first
// then files, both alphabetical (case-insensitive) — dotfiles (.git,
// .DS_Store, an editor's swap file) are never listed, since nothing
// under the library root should need one.
func (s *Service) ListDirectory(relPath string) (Listing, error) {
	full, err := s.resolve(relPath)
	if err != nil {
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

// OpenFile opens relPath for reading. The caller is responsible for
// closing File.Content.
func (s *Service) OpenFile(relPath string) (File, error) {
	full, err := s.resolve(relPath)
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

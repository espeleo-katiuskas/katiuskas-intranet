package activityresources

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Storage holds a file resource's raw bytes on disk, one file per
// resource id under a per-activity subdirectory — unlike
// internal/library or internal/userfiles, nothing here is ever a
// caller-given path: activityID/resourceID always come from either a
// validated URL path segment (parsed as an integer — never a string
// that could contain "..") or a database row this package's own
// Repository just wrote, so there's no path-escape risk to guard
// against and no Service.resolve equivalent needed.
type Storage struct {
	root string
}

// NewStorage resolves root once at startup, creating it if it doesn't
// exist yet, the same as library.NewService/userfiles.NewService.
func NewStorage(root string) (*Storage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create activity resources root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Storage{root: resolved}, nil
}

func (s *Storage) pathFor(activityID, resourceID int64) string {
	return filepath.Join(s.root, strconv.FormatInt(activityID, 10), strconv.FormatInt(resourceID, 10))
}

// Save writes content to disk under activityID/resourceID, creating
// that activity's own subdirectory the first time one of its resources
// gets a file. It refuses (os.ErrExist) to touch a file that's already
// there: every resourceID is only ever saved to once, and a file's bytes
// are never modified afterward (see UpdateInput's own doc comment), so
// an existing file means something other than this resource put it
// there. Files that are hard-linked from another directory tree, such
// as a copy of production's files made with `cp -al`, rely on exactly
// this — truncating one in place would silently change every tree
// linked to it.
func (s *Storage) Save(activityID, resourceID int64, content io.Reader) error {
	dir := filepath.Join(s.root, strconv.FormatInt(activityID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := s.pathFor(activityID, resourceID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, content); err != nil {
		f.Close()
		// Best-effort: an upload that fails partway (the client
		// disconnecting, a read error off the request body) used to
		// leave a partial file behind here — Service.CreateFile's own
		// cleanup on a Save failure only ever deletes the metadata row
		// it just created, never reaches into storage at all, so
		// nothing else was going to catch this.
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// Open opens a resource's file for reading. The caller is responsible
// for closing it.
func (s *Storage) Open(activityID, resourceID int64) (*os.File, error) {
	return os.Open(s.pathFor(activityID, resourceID))
}

func (s *Storage) Delete(activityID, resourceID int64) error {
	return os.Remove(s.pathFor(activityID, resourceID))
}

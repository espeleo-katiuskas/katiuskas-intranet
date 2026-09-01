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
// gets a file. Overwrites anything already there, though in practice
// every resourceID is only ever saved to once (a file resource's bytes
// are never replaced in place — see UpdateInput's own doc comment).
func (s *Storage) Save(activityID, resourceID int64, content io.Reader) error {
	dir := filepath.Join(s.root, strconv.FormatInt(activityID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(s.pathFor(activityID, resourceID))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, content)
	return err
}

// Open opens a resource's file for reading. The caller is responsible
// for closing it.
func (s *Storage) Open(activityID, resourceID int64) (*os.File, error) {
	return os.Open(s.pathFor(activityID, resourceID))
}

func (s *Storage) Delete(activityID, resourceID int64) error {
	return os.Remove(s.pathFor(activityID, resourceID))
}

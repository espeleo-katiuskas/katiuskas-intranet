package library

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Storage holds the bytes of the files on disk, one file per row of library_resources under a
// subdirectory per item, the way internal/transactionfiles keeps its files. Nothing here is ever a
// caller-given path: both ids are integers parsed from the URL or read from a row this package wrote,
// so there is no path to escape from.
type Storage struct {
	root string
}

// NewStorage resolves root once at startup, creating it if it does not exist yet.
func NewStorage(root string) (*Storage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create item files root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Storage{root: resolved}, nil
}

func (s *Storage) pathFor(itemID, resourceID int64) string {
	return filepath.Join(s.root, strconv.FormatInt(itemID, 10), strconv.FormatInt(resourceID, 10))
}

// Save writes content under itemID/resourceID. It refuses (os.ErrExist) to touch a file that is
// already there: every id is saved to once and the bytes are never modified afterwards, so an
// existing file means something else put it there. A copy of production's files made of hard links
// (scripts/copy-prod-to-testing.sh) relies on exactly this: truncating one in place would change
// every tree linked to it. Replacing a cover is a new row and a new file, never a rewrite.
func (s *Storage) Save(itemID, resourceID int64, content io.Reader) error {
	dir := filepath.Join(s.root, strconv.FormatInt(itemID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := s.pathFor(itemID, resourceID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, content); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// Open opens a file for reading; the caller closes it.
func (s *Storage) Open(itemID, resourceID int64) (*os.File, error) {
	return os.Open(s.pathFor(itemID, resourceID))
}

func (s *Storage) Delete(itemID, resourceID int64) error {
	return os.Remove(s.pathFor(itemID, resourceID))
}

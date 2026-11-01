package transactionfiles

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Service struct {
	repo    *Repository
	storage *Storage
}

func NewService(repo *Repository, storage *Storage) *Service {
	return &Service{repo: repo, storage: storage}
}

// inlineTypes are the only types a browser is allowed to show inside the page; anything else is
// offered as a download, so a file can never run as a page of the intranet.
var inlineTypes = map[string]bool{
	"application/pdf": true, "image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// Viewable says whether a stored content type is one the browser shows instead of downloading.
func Viewable(contentType string) bool { return inlineTypes[contentType] }

// detectType decides a file's type from its bytes: a PDF or an image is recognised by content, so
// it can be shown; for anything else the name's extension gives a type that is only ever downloaded.
func detectType(name string, content []byte) string {
	sniffed, _, _ := strings.Cut(http.DetectContentType(content), ";")
	if inlineTypes[sniffed] {
		return sniffed
	}
	if byExt, _, err := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(name))); err == nil && !inlineTypes[byExt] {
		return byExt
	}
	return "application/octet-stream"
}

func (s *Service) ListByTransaction(ctx context.Context, transactionID int64) ([]File, error) {
	if _, err := s.repo.TransactionDescription(ctx, transactionID); err != nil {
		return nil, err
	}
	return s.repo.ListByTransaction(ctx, transactionID)
}

// Add attaches a file and returns it with the description of the transaction. The bytes are
// saved only after the row exists, since their path is keyed by the row's id, and the row is
// removed again if the save fails, so that a file whose bytes are not there never stays listed.
func (s *Service) Add(ctx context.Context, transactionID, uploadedBy int64, name string, content []byte) (File, string, error) {
	description, err := s.repo.TransactionDescription(ctx, transactionID)
	if err != nil {
		return File{}, "", err
	}
	if len(content) == 0 {
		return File{}, "", ErrEmptyFile
	}
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	f, err := s.repo.Create(ctx, transactionID, uploadedBy, name, detectType(name, content), len(content))
	if err != nil {
		return File{}, "", err
	}
	if err := s.storage.Save(transactionID, f.ID, bytes.NewReader(content)); err != nil {
		_ = s.repo.Delete(ctx, transactionID, f.ID)
		return File{}, "", err
	}
	return f, description, nil
}

// Open is a file's metadata and its open file, which the caller closes.
func (s *Service) Open(ctx context.Context, transactionID, id int64) (File, *os.File, error) {
	f, err := s.repo.Get(ctx, transactionID, id)
	if err != nil {
		return File{}, nil, err
	}
	file, err := s.storage.Open(transactionID, id)
	if err != nil {
		return File{}, nil, err
	}
	return f, file, nil
}

// Remove deletes a file and returns it, with the description of its transaction, for the notice.
func (s *Service) Remove(ctx context.Context, transactionID, id int64) (File, string, error) {
	f, err := s.repo.Get(ctx, transactionID, id)
	if err != nil {
		return File{}, "", err
	}
	description, err := s.repo.TransactionDescription(ctx, transactionID)
	if err != nil {
		return File{}, "", err
	}
	if err := s.repo.Delete(ctx, transactionID, id); err != nil {
		return File{}, "", err
	}
	// The row is gone, so the file no longer exists for anybody; failing to remove its bytes only
	// leaves an unreferenced file behind and must not undo what was asked.
	if err := s.storage.Delete(transactionID, id); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("remove a transaction file from disk", "error", err, "transaction_id", transactionID, "file_id", id)
	}
	return f, description, nil
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Listing, error) {
	return s.repo.List(ctx, filter)
}

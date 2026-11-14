package places

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ----- repository -----

const fileSelect = `
	SELECT f.id, f.place_id, f.title, f.file_name, f.content_type, f.file_size, u.login, f.created_at
	FROM place_files f JOIN users u ON u.id = f.uploaded_by`

func scanFile(row pgx.Row, f *File) error {
	return row.Scan(&f.ID, &f.PlaceID, &f.Title, &f.FileName, &f.ContentType, &f.FileSize,
		&f.UploadedBy, &f.CreatedAt)
}

// ListFiles is the files of a place, oldest first.
func (r *Repository) ListFiles(ctx context.Context, placeID int64) ([]File, error) {
	rows, err := r.pool.Query(ctx, fileSelect+` WHERE f.place_id = $1 ORDER BY f.id`, placeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []File{}
	for rows.Next() {
		var f File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

// GetFile is a file of the place and the id of whoever attached it.
func (r *Repository) GetFile(ctx context.Context, placeID, id int64) (File, int64, error) {
	var f File
	var uploaderID int64
	err := r.pool.QueryRow(ctx, `
		SELECT f.id, f.place_id, f.title, f.file_name, f.content_type, f.file_size, u.login, f.created_at,
			f.uploaded_by
		FROM place_files f JOIN users u ON u.id = f.uploaded_by
		WHERE f.place_id = $1 AND f.id = $2
	`, placeID, id).Scan(&f.ID, &f.PlaceID, &f.Title, &f.FileName, &f.ContentType, &f.FileSize,
		&f.UploadedBy, &f.CreatedAt, &uploaderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, 0, ErrFileNotFound
	}
	return f, uploaderID, err
}

// CreateFile adds the row of a file, and touches the place so that it says who changed it last.
func (r *Repository) CreateFile(ctx context.Context, placeID, uploadedBy int64, title *string,
	name, contentType string, size int) (File, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, placeID); err != nil {
		return File{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO place_files (place_id, title, file_name, content_type, file_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, placeID, title, name, contentType, size, uploadedBy).Scan(&id)
	if err != nil {
		return File{}, err
	}
	if err := touch(ctx, tx, placeID, uploadedBy); err != nil {
		return File{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return File{}, err
	}
	var f File
	err = scanFile(r.pool.QueryRow(ctx, fileSelect+` WHERE f.id = $1`, id), &f)
	return f, err
}

// DeleteFile removes the row of a file, and touches the place like CreateFile does.
func (r *Repository) DeleteFile(ctx context.Context, placeID, id, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, placeID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM place_files WHERE place_id = $1 AND id = $2`, placeID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFileNotFound
	}
	if err := touch(ctx, tx, placeID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----- service -----

// cleanName keeps only the last element of the name a browser sent, which may be a whole path.
func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	return name
}

// removeBytes deletes a file's bytes once its row is gone: the row was the only thing that made the
// file exist for anybody, so failing to remove the bytes only leaves an unreferenced file behind and
// must not undo what was asked.
func (s *Service) removeBytes(placeID, fileID int64) {
	if err := s.storage.Delete(placeID, fileID); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("remove a place file from disk", "error", err, "place_id", placeID, "file_id", fileID)
	}
}

func (s *Service) ListFiles(ctx context.Context, placeID int64) ([]File, error) {
	if _, err := s.repo.GetPlace(ctx, placeID); err != nil {
		return nil, err
	}
	return s.repo.ListFiles(ctx, placeID)
}

// AddFile attaches a file (anything) to a place, with an optional caption. The row is created first,
// since the path of the bytes is keyed by its id, and removed again if the save fails, so that a file
// whose bytes are not there never stays listed.
func (s *Service) AddFile(ctx context.Context, placeID, uploadedBy int64, title *string, name string, content []byte) (File, error) {
	if len(content) == 0 {
		return File{}, ErrEmptyFile
	}
	title = blankToNil(title)
	if title != nil && utf8.RuneCountInString(*title) > MaxFileTitleLength {
		return File{}, ErrTitleTooLong
	}
	name = cleanName(name)
	f, err := s.repo.CreateFile(ctx, placeID, uploadedBy, title, name, detectType(name, content), len(content))
	if err != nil {
		return File{}, err
	}
	if err := s.storage.Save(placeID, f.ID, bytes.NewReader(content)); err != nil {
		_ = s.repo.DeleteFile(ctx, placeID, f.ID, uploadedBy)
		return File{}, err
	}
	return f, nil
}

// RemoveFile deletes a file: whoever attached it may, and so may admin and board (privileged). It
// answers with the file, for the message that tells who removed what.
func (s *Service) RemoveFile(ctx context.Context, placeID, id, requesterID int64, privileged bool) (File, error) {
	f, uploaderID, err := s.repo.GetFile(ctx, placeID, id)
	if err != nil {
		return File{}, err
	}
	if !privileged && uploaderID != requesterID {
		return File{}, ErrNotYours
	}
	if err := s.repo.DeleteFile(ctx, placeID, id, requesterID); err != nil {
		return File{}, err
	}
	s.removeBytes(placeID, id)
	return f, nil
}

// OpenFile is a file's metadata and its open file, which the caller closes.
func (s *Service) OpenFile(ctx context.Context, placeID, id int64) (File, *os.File, error) {
	f, _, err := s.repo.GetFile(ctx, placeID, id)
	if err != nil {
		return File{}, nil, err
	}
	file, err := s.storage.Open(placeID, id)
	if err != nil {
		return File{}, nil, err
	}
	return f, file, nil
}

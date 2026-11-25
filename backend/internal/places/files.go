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

// fileOwner says whose a file is: a place's or a system's. They share the table `place_files` and the
// directory on disk (a file is at <owner id>/<row id>, and a row id is unique among all of them), so
// the code that stores, lists, serves and removes files is the same for both.
type fileOwner struct {
	column string // "place_id" or "system_id": a constant, never from a caller.
	id     int64
}

func placeOwner(id int64) fileOwner  { return fileOwner{"place_id", id} }
func systemOwner(id int64) fileOwner { return fileOwner{"system_id", id} }

func (o fileOwner) isSystem() bool { return o.column == "system_id" }

// lock locks the owner's row for the rest of the transaction, or says it is not there.
func (o fileOwner) lock(ctx context.Context, tx pgx.Tx) error {
	if o.isSystem() {
		return lockSystem(ctx, tx, o.id)
	}
	return lockPlace(ctx, tx, o.id)
}

// touch records who changed the owner last, and when.
func (o fileOwner) touch(ctx context.Context, tx pgx.Tx, by int64) error {
	if o.isSystem() {
		return touchSystem(ctx, tx, o.id, by)
	}
	return touch(ctx, tx, o.id, by)
}

// ----- repository -----

const fileSelect = `
	SELECT f.id, f.place_id, f.system_id, f.title, f.file_name, f.content_type, f.file_size, a.display_name,
		f.created_at, f.uploaded_by
	FROM catalogue.place_files f JOIN catalogue.accounts a ON a.id = f.uploaded_by`

func scanFile(row pgx.Row, f *File) error {
	return row.Scan(&f.ID, &f.PlaceID, &f.SystemID, &f.Title, &f.FileName, &f.ContentType, &f.FileSize,
		&f.UploadedBy, &f.CreatedAt, &f.UploadedByID)
}

// ListFiles is the files of a place or a system, oldest first.
func (r *Repository) ListFiles(ctx context.Context, o fileOwner) ([]File, error) {
	rows, err := r.pool.Query(ctx, fileSelect+` WHERE f.`+o.column+` = $1 ORDER BY f.id`, o.id)
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

// GetFile is a file of the owner and the id of whoever attached it.
func (r *Repository) GetFile(ctx context.Context, o fileOwner, id int64) (File, int64, error) {
	var f File
	err := scanFile(r.pool.QueryRow(ctx, fileSelect+` WHERE f.`+o.column+` = $1 AND f.id = $2`, o.id, id), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, 0, ErrFileNotFound
	}
	return f, f.UploadedByID, err
}

// CreateFile adds the row of a file, and touches the owner so that it says who changed it last.
func (r *Repository) CreateFile(ctx context.Context, o fileOwner, uploadedBy int64, title *string,
	name, contentType string, size int) (File, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback(ctx)
	if err := o.lock(ctx, tx); err != nil {
		return File{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO catalogue.place_files (`+o.column+`, title, file_name, content_type, file_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, o.id, title, name, contentType, size, uploadedBy).Scan(&id)
	if err != nil {
		return File{}, err
	}
	if err := o.touch(ctx, tx, uploadedBy); err != nil {
		return File{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return File{}, err
	}
	var f File
	err = scanFile(r.pool.QueryRow(ctx, fileSelect+` WHERE f.id = $1`, id), &f)
	return f, err
}

// DeleteFile removes the row of a file, and touches the owner like CreateFile does.
func (r *Repository) DeleteFile(ctx context.Context, o fileOwner, id int64, by int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := o.lock(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM catalogue.place_files WHERE `+o.column+` = $1 AND id = $2`, o.id, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFileNotFound
	}
	if err := o.touch(ctx, tx, by); err != nil {
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
func (s *Service) removeBytes(ownerID, fileID int64) {
	if err := s.storage.Delete(ownerID, fileID); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("remove a file from disk", "error", err, "owner_id", ownerID, "file_id", fileID)
	}
}

// ownerExists says that the place or the system is there, or that it is not (ErrNotFound for a place,
// ErrSystemNotFound for a system).
func (s *Service) ownerExists(ctx context.Context, o fileOwner) error {
	if o.isSystem() {
		if exists, err := s.repo.SystemExists(ctx, o.id); err != nil {
			return err
		} else if !exists {
			return ErrSystemNotFound
		}
		return nil
	}
	_, err := s.repo.GetPlace(ctx, o.id)
	return err
}

func (s *Service) ListFiles(ctx context.Context, o fileOwner) ([]File, error) {
	if err := s.ownerExists(ctx, o); err != nil {
		return nil, err
	}
	return s.repo.ListFiles(ctx, o)
}

// AddFile attaches a file (anything) to a place or a system, with an optional caption. The row is
// created first, since the path of the bytes is keyed by its id, and removed again if the save fails, so
// that a file whose bytes are not there never stays listed.
func (s *Service) AddFile(ctx context.Context, o fileOwner, uploadedBy int64, title *string, name string, content []byte) (File, error) {
	if len(content) == 0 {
		return File{}, ErrEmptyFile
	}
	title = blankToNil(title)
	if title != nil && utf8.RuneCountInString(*title) > MaxFileTitleLength {
		return File{}, ErrTitleTooLong
	}
	name = cleanName(name)
	f, err := s.repo.CreateFile(ctx, o, uploadedBy, title, name, detectType(name, content), len(content))
	if err != nil {
		return File{}, err
	}
	if err := s.storage.Save(o.id, f.ID, bytes.NewReader(content)); err != nil {
		_ = s.repo.DeleteFile(ctx, o, f.ID, uploadedBy)
		return File{}, err
	}
	return f, nil
}

// RemoveFile deletes a file: whoever attached it may, and so may admin and board (privileged). It
// answers with the file, for the message that tells who removed what.
func (s *Service) RemoveFile(ctx context.Context, o fileOwner, id int64, requesterID int64, privileged bool) (File, error) {
	f, uploaderID, err := s.repo.GetFile(ctx, o, id)
	if err != nil {
		return File{}, err
	}
	if !privileged && uploaderID != requesterID {
		return File{}, ErrNotYours
	}
	if err := s.repo.DeleteFile(ctx, o, id, requesterID); err != nil {
		return File{}, err
	}
	s.removeBytes(o.id, id)
	return f, nil
}

// OpenFile is a file's metadata and its open file, which the caller closes.
func (s *Service) OpenFile(ctx context.Context, o fileOwner, id int64) (File, *os.File, error) {
	f, _, err := s.repo.GetFile(ctx, o, id)
	if err != nil {
		return File{}, nil, err
	}
	file, err := s.storage.Open(o.id, id)
	if err != nil {
		return File{}, nil, err
	}
	return f, file, nil
}

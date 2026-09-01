package activityresources

import (
	"context"
	"errors"
	"io"
	"os"
)

// ErrNotAFile means OpenFile was called on a text resource — there's
// nothing on disk for one of those.
var ErrNotAFile = errors.New("this resource has no file")

// ErrDescriptionNotAllowed means UpdateInput.Description was given for
// a file resource — Repository.Update would also reject this via the
// database's own CHECK constraint (ErrInvalidFields), but checking here
// first gives a clearer, type-specific error instead of relying on that
// generic one.
var ErrDescriptionNotAllowed = errors.New("only a text resource can have a description")

type Service struct {
	repo    *Repository
	storage *Storage
}

func NewService(repo *Repository, storage *Storage) *Service {
	return &Service{repo: repo, storage: storage}
}

func (s *Service) ListByActivity(ctx context.Context, activityID int64) ([]Resource, error) {
	return s.repo.ListByActivity(ctx, activityID)
}

func (s *Service) Get(ctx context.Context, id int64) (Resource, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) CreateText(ctx context.Context, activityID, authorID int64, title, description string) (Resource, error) {
	return s.repo.Create(ctx, CreateInput{
		ActivityID: activityID, Type: ResourceText, AuthorID: authorID, Title: title, Description: &description,
	})
}

// CreateFile saves content to disk only *after* the metadata row
// exists, since the storage path is keyed by that row's own id — and
// deletes the row again if the save then fails, since Postgres can't
// span a transaction across the filesystem the way it would across two
// tables. A resource whose row exists but whose file never made it to
// disk would otherwise 404 confusingly on every later attempt to open
// it, instead of the create itself having failed up front.
func (s *Service) CreateFile(ctx context.Context, activityID, authorID int64, title, fileName, contentType string, fileSize int64, content io.Reader) (Resource, error) {
	res, err := s.repo.Create(ctx, CreateInput{
		ActivityID: activityID, Type: ResourceFile, AuthorID: authorID, Title: title,
		FileName: &fileName, FileSize: &fileSize, ContentType: &contentType,
	})
	if err != nil {
		return Resource{}, err
	}
	if err := s.storage.Save(activityID, res.ID, content); err != nil {
		_ = s.repo.Delete(ctx, res.ID)
		return Resource{}, err
	}
	return res, nil
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Resource, error) {
	if in.Description != nil {
		res, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return Resource{}, err
		}
		if res.Type != ResourceText {
			return Resource{}, ErrDescriptionNotAllowed
		}
	}
	return s.repo.Update(ctx, id, in)
}

// Delete removes the metadata row first — once that's gone, the
// resource no longer exists as far as any caller can tell — then best-
// effort removes its file from disk, if it has one; a failure there
// (already missing, a permissions hiccup) leaves an orphaned file
// behind but never blocks the delete the caller actually asked for.
func (s *Service) Delete(ctx context.Context, id int64) error {
	res, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if res.Type == ResourceFile {
		_ = s.storage.Delete(res.ActivityID, res.ID)
	}
	return nil
}

// OpenFile returns a file resource's metadata alongside its open file —
// ErrNotAFile for a text resource, which never has one. The caller is
// responsible for closing it.
func (s *Service) OpenFile(ctx context.Context, id int64) (Resource, *os.File, error) {
	res, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Resource{}, nil, err
	}
	if res.Type != ResourceFile {
		return Resource{}, nil, ErrNotAFile
	}
	f, err := s.storage.Open(res.ActivityID, res.ID)
	if err != nil {
		return Resource{}, nil, err
	}
	return res, f, nil
}

func (s *Service) Reorder(ctx context.Context, activityID int64, orderedIDs []int64) error {
	return s.repo.Reorder(ctx, activityID, orderedIDs)
}

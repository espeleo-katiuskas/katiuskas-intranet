package clubinfo

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidLabel = errors.New("label is required and can't be longer than 100 characters")
	ErrInvalidValue = errors.New("value is required and can't be longer than 500 characters")
	ErrInvalidCopy  = errors.New("copy_value can't be longer than 500 characters, and copy_label (up to 100) needs a copy_value")
)

const (
	maxLabel = 100
	maxText  = 500
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// normalize trims what was typed and turns a blank copy_value or copy_label into none, since a
// button that copies nothing (or has no text) means nothing.
func normalize(in Input) (Input, error) {
	in.Label = strings.TrimSpace(in.Label)
	in.Value = strings.TrimSpace(in.Value)
	in.CopyValue = blankToNil(in.CopyValue)
	in.CopyLabel = blankToNil(in.CopyLabel)
	if in.Label == "" || utf8.RuneCountInString(in.Label) > maxLabel {
		return in, ErrInvalidLabel
	}
	if in.Value == "" || utf8.RuneCountInString(in.Value) > maxText {
		return in, ErrInvalidValue
	}
	if in.CopyLabel != nil && in.CopyValue == nil {
		return in, ErrInvalidCopy
	}
	if in.CopyValue != nil && utf8.RuneCountInString(*in.CopyValue) > maxText {
		return in, ErrInvalidCopy
	}
	if in.CopyLabel != nil && utf8.RuneCountInString(*in.CopyLabel) > maxLabel {
		return in, ErrInvalidCopy
	}
	return in, nil
}

func blankToNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func (s *Service) List(ctx context.Context) ([]Item, error) { return s.repo.List(ctx) }

func (s *Service) Get(ctx context.Context, id int64) (Item, error) { return s.repo.Get(ctx, id) }

func (s *Service) Create(ctx context.Context, in Input) (Item, error) {
	in, err := normalize(in)
	if err != nil {
		return Item{}, err
	}
	return s.repo.Create(ctx, in)
}

func (s *Service) Update(ctx context.Context, id int64, in Input) (Item, error) {
	in, err := normalize(in)
	if err != nil {
		return Item{}, err
	}
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Delete(ctx context.Context, id int64) error { return s.repo.Delete(ctx, id) }

func (s *Service) Reorder(ctx context.Context, orderedIDs []int64) error {
	return s.repo.Reorder(ctx, orderedIDs)
}

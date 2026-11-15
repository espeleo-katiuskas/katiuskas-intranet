package areas

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Children are the zones directly below a zone, or the countries if parentID is nil.
func (s *Service) Children(ctx context.Context, parentID *int64) ([]Area, error) {
	list, err := s.repo.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 && parentID != nil {
		// Nothing below it: either it is a leaf or it does not exist.
		if _, err := s.repo.Get(ctx, *parentID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// SearchLimit is how many zones a search gives at most, and how many by default.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 50
)

// Search is the zones whose name contains the text, ignoring case, accents and punctuation.
func (s *Service) Search(ctx context.Context, text string, limit int) ([]Area, error) {
	normalized := Normalize(text)
	if normalized == "" {
		return []Area{}, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	return s.repo.Search(ctx, normalized, min(limit, MaxSearchLimit))
}

func (s *Service) Get(ctx context.Context, id int64) (Area, error) { return s.repo.Get(ctx, id) }

// cleanText checks a name and a kind (which may be blank, to be filled in) and normalizes the name.
func cleanText(name, kind string) (string, string, string, error) {
	name, kind = strings.TrimSpace(name), strings.TrimSpace(kind)
	normalized := Normalize(name)
	if name == "" || normalized == "" {
		return "", "", "", ErrBlankName
	}
	if utf8.RuneCountInString(name) > MaxNameLength || utf8.RuneCountInString(kind) > MaxKindLength {
		return "", "", "", ErrTooLong
	}
	return name, kind, normalized, nil
}

// Create adds a zone below another, or a country when parentID is nil. Its kind is the one the zones
// below that one already have, unless it is given; with none below it yet, it has to be given; a country
// is a "País" unless it is said otherwise.
func (s *Service) Create(ctx context.Context, parentID *int64, name, kind string) (Area, error) {
	name, kind, normalized, err := cleanText(name, kind)
	if err != nil {
		return Area{}, err
	}
	if parentID != nil {
		if _, err := s.repo.Get(ctx, *parentID); errors.Is(err, ErrNotFound) {
			return Area{}, ErrUnknownParent
		} else if err != nil {
			return Area{}, err
		}
	}
	if kind == "" {
		if parentID == nil {
			kind = CountryKind
		} else {
			if kind, err = s.repo.KindBelow(ctx, *parentID); err != nil {
				return Area{}, err
			}
			if kind == "" {
				return Area{}, ErrKindRequired
			}
		}
	}
	taken, err := s.repo.NameTaken(ctx, parentID, normalized, 0)
	if err != nil {
		return Area{}, err
	}
	if taken {
		return Area{}, ErrDuplicate
	}
	return s.repo.Create(ctx, parentID, kind, name, normalized)
}

// Update renames a zone and, if a kind is given, changes it; with move.Set it also puts it below another
// zone (or makes it a country). Nothing changes if any part is refused.
func (s *Service) Update(ctx context.Context, id int64, name, kind string, move Move) (Area, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return Area{}, err
	}
	name, kind, normalized, err := cleanText(name, kind)
	if err != nil {
		return Area{}, err
	}
	if kind == "" {
		kind = current.Kind
	}
	target := current.ParentID
	if move.Set {
		target = move.ID
		if target != nil {
			if *target == id {
				return Area{}, ErrMoveCycle
			}
			if _, err := s.repo.Get(ctx, *target); errors.Is(err, ErrNotFound) {
				return Area{}, ErrUnknownParent
			} else if err != nil {
				return Area{}, err
			}
		}
	}
	taken, err := s.repo.NameTaken(ctx, target, normalized, id)
	if err != nil {
		return Area{}, err
	}
	if taken {
		return Area{}, ErrDuplicate
	}
	return s.repo.Update(ctx, id, kind, name, normalized, move)
}

// Delete removes a zone that has no zones below it and no places. A country is never deleted.
func (s *Service) Delete(ctx context.Context, id int64) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case a.ParentID == nil:
		return ErrCountry
	case a.ChildCount > 0:
		return ErrHasChildren
	case a.PlaceCount > 0:
		return ErrInUse
	}
	return s.repo.Delete(ctx, id)
}

// Merge moves everything in a zone to another below the same one, and deletes the first.
func (s *Service) Merge(ctx context.Context, from, into int64) error {
	if from == into {
		return ErrMergeSelf
	}
	return s.repo.Merge(ctx, from, into)
}

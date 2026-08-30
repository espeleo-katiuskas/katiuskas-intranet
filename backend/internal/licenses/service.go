package licenses

import (
	"context"
	"errors"

	"katiuskas-intranet/backend/internal/memberships"
)

// ErrNotActiveMember means the target person doesn't currently have an
// open membership period — only someone who hasn't caused baja can be
// assigned a federative license.
var ErrNotActiveMember = errors.New("only a currently active member can be assigned a federative license")

type Service struct {
	repo        *Repository
	memberships *memberships.Service
}

func NewService(repo *Repository, membershipsService *memberships.Service) *Service {
	return &Service{repo: repo, memberships: membershipsService}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (License, error) {
	active, err := s.memberships.IsActiveMember(ctx, in.UserID)
	if err != nil {
		return License{}, err
	}
	if !active {
		return License{}, ErrNotActiveMember
	}
	return s.repo.Create(ctx, in)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]License, error) {
	return s.repo.ListByUser(ctx, userID)
}

// LatestByUser returns the person's most recently issued license, or nil
// if they've never had one — used to show a "current" license on the
// people list without a separate per-person round trip for full history.
func (s *Service) LatestByUser(ctx context.Context, userID int64) (*License, error) {
	list, err := s.repo.ListByUser(ctx, userID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

package memberships

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Membership, error) {
	return s.repo.Create(ctx, in)
}

func (s *Service) End(ctx context.Context, id int64, in EndInput) (Membership, error) {
	return s.repo.End(ctx, id, in)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) ListActive(ctx context.Context) ([]Membership, error) {
	return s.repo.ListActive(ctx)
}

package board

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Member, error) {
	return s.repo.Create(ctx, in)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Member, error) {
	return s.repo.Update(ctx, id, in)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Member, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) ListActive(ctx context.Context) ([]Member, error) {
	return s.repo.ListActive(ctx)
}

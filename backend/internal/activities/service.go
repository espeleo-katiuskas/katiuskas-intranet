package activities

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Activity, error) {
	return s.repo.Create(ctx, in)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]Activity, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.List(ctx, limit, offset)
}

func (s *Service) ListActive(ctx context.Context, limit, offset int) ([]Activity, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListActive(ctx, limit, offset)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Activity, error) {
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Cancel(ctx context.Context, id int64) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Join(ctx context.Context, activityID, userID int64) error {
	return s.repo.AddParticipant(ctx, activityID, userID)
}

func (s *Service) Leave(ctx context.Context, activityID, userID int64) error {
	return s.repo.RemoveParticipant(ctx, activityID, userID)
}

func (s *Service) ListParticipants(ctx context.Context, activityID int64) ([]Participant, error) {
	return s.repo.ListParticipants(ctx, activityID)
}

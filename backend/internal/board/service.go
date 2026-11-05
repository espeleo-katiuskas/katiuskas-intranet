package board

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Member, error) {
	if !in.Position.Valid() {
		return Member{}, ErrInvalidPosition
	}
	return s.repo.Create(ctx, in)
}

// Get returns a single board term by id — used by Handler's own
// mutation notification (a before/after diff on Update), not by any
// route of its own.
func (s *Service) Get(ctx context.Context, id int64) (Member, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Member, error) {
	if !in.Position.Valid() {
		return Member{}, ErrInvalidPosition
	}
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Member, error) {
	return s.repo.ListByUser(ctx, userID)
}

// ListAll is the whole history of the board (see Repository.ListAll).
func (s *Service) ListAll(ctx context.Context) ([]Member, error) {
	return s.repo.ListAll(ctx)
}

func (s *Service) ListActive(ctx context.Context) ([]Member, error) {
	return s.repo.ListActive(ctx)
}

package inventory

import (
	"context"
	"time"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	return s.repo.CreateItem(ctx, in)
}

func (s *Service) ListItems(ctx context.Context) ([]Item, error) {
	return s.repo.ListItems(ctx)
}

func (s *Service) ListMaterialCategories(ctx context.Context) ([]MaterialCategory, error) {
	return s.repo.ListMaterialCategories(ctx)
}

func (s *Service) CreateMovement(ctx context.Context, in CreateMovementInput) (Movement, error) {
	if in.Quantity <= 0 {
		in.Quantity = 1
	}
	if in.TransactedAt.IsZero() {
		in.TransactedAt = time.Now()
	}
	return s.repo.CreateMovement(ctx, in)
}

func (s *Service) ReturnMovement(ctx context.Context, id int64) (Movement, error) {
	return s.repo.ReturnMovement(ctx, id)
}

func (s *Service) ListMovementsByItem(ctx context.Context, itemID int64) ([]Movement, error) {
	return s.repo.ListMovementsByItem(ctx, itemID)
}

func (s *Service) ListMovementsByStatus(ctx context.Context, status MovementStatus) ([]Movement, error) {
	return s.repo.ListMovementsByStatus(ctx, status)
}

func (s *Service) ListMovementsByUser(ctx context.Context, userID int64) ([]Movement, error) {
	return s.repo.ListMovementsByUser(ctx, userID)
}

package inventory

import "context"

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

func (s *Service) CreateTransaction(ctx context.Context, in CreateTransactionInput) (Transaction, error) {
	if in.Quantity <= 0 {
		in.Quantity = 1
	}
	return s.repo.CreateTransaction(ctx, in)
}

func (s *Service) ReturnTransaction(ctx context.Context, id int64) (Transaction, error) {
	return s.repo.ReturnTransaction(ctx, id)
}

func (s *Service) ListTransactionsByItem(ctx context.Context, itemID int64) ([]Transaction, error) {
	return s.repo.ListTransactionsByItem(ctx, itemID)
}

func (s *Service) ListTransactionsByStatus(ctx context.Context, status TransactionStatus) ([]Transaction, error) {
	return s.repo.ListTransactionsByStatus(ctx, status)
}

func (s *Service) ListTransactionsByUser(ctx context.Context, userID int64) ([]Transaction, error) {
	return s.repo.ListTransactionsByUser(ctx, userID)
}

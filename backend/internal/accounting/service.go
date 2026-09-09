package accounting

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error) {
	return s.repo.CreateAccount(ctx, in)
}

func (s *Service) GetAccount(ctx context.Context, id int64) (Account, error) {
	return s.repo.GetAccount(ctx, id)
}

func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	return s.repo.ListAccounts(ctx)
}

func (s *Service) UpdateAccount(ctx context.Context, id int64, in UpdateAccountInput) (Account, error) {
	return s.repo.UpdateAccount(ctx, id, in)
}

func (s *Service) DeleteAccount(ctx context.Context, id int64) error {
	return s.repo.DeleteAccount(ctx, id)
}

func (s *Service) CreateTransaction(ctx context.Context, in CreateTransactionInput) (Transaction, error) {
	return s.repo.CreateTransaction(ctx, in)
}

// GetTransaction returns a single transaction by id — used by
// Handler's own mutation notification (a before/after diff on Update),
// not just by GET /transactions/{id}.
func (s *Service) GetTransaction(ctx context.Context, id int64) (Transaction, error) {
	return s.repo.GetTransaction(ctx, id)
}

func (s *Service) ListTransactions(ctx context.Context, limit, offset int) ([]Transaction, error) {
	return s.repo.ListTransactions(ctx, limit, offset)
}

func (s *Service) UpdateTransaction(ctx context.Context, id int64, in UpdateTransactionInput) (Transaction, error) {
	return s.repo.UpdateTransaction(ctx, id, in)
}

func (s *Service) DeleteTransaction(ctx context.Context, id int64) error {
	return s.repo.DeleteTransaction(ctx, id)
}

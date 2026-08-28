package users

import (
	"context"
	"time"

	"katiuskas-intranet/backend/internal/auth"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	return s.repo.Create(ctx, in, hash)
}

func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.List(ctx, limit, offset)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (User, error) {
	return s.repo.Update(ctx, id, in)
}

func (s *Service) VerifyCredentials(ctx context.Context, username, password string) (Credentials, error) {
	creds, err := s.repo.GetCredentialsByUsername(ctx, username)
	if err != nil {
		return creds, err
	}
	if !creds.IsActive || !auth.VerifyPassword(creds.PasswordHash, password) {
		return creds, auth.ErrInvalidCredentials
	}
	return creds, nil
}

func (s *Service) GetCredentialsByUserID(ctx context.Context, userID int64) (Credentials, error) {
	return s.repo.GetCredentialsByUserID(ctx, userID)
}

func (s *Service) TouchLastLogin(ctx context.Context, userID int64) error {
	return s.repo.TouchLastLogin(ctx, userID)
}

func (s *Service) SaveRefreshToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	return s.repo.SaveRefreshToken(ctx, userID, tokenHash, expiresAt)
}

func (s *Service) ResolveRefreshToken(ctx context.Context, tokenHash string) (int64, error) {
	return s.repo.GetRefreshToken(ctx, tokenHash)
}

func (s *Service) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	return s.repo.RevokeRefreshToken(ctx, tokenHash)
}

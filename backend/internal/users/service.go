package users

import (
	"context"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/roles"
)

type Service struct {
	repo  *Repository
	roles *roles.Service
}

func NewService(repo *Repository, rolesService *roles.Service) *Service {
	return &Service{repo: repo, roles: rolesService}
}

// Create inserts the person and their credentials, then assigns their
// roles as a separate step (defaulting to "member" if none were given).
// That second step isn't part of the same database transaction as the
// insert — a club-scale app doesn't need cross-package transactions for
// this, but it does mean a role-assignment failure leaves a person record
// behind with no roles rather than rolling back the whole creation.
func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	user, err := s.repo.Create(ctx, in, hash)
	if err != nil {
		return user, err
	}

	roleNames := in.RoleNames
	if len(roleNames) == 0 {
		roleNames = []string{"member"}
	}
	if err := s.roles.AssignByName(ctx, user.ID, roleNames); err != nil {
		return user, err
	}
	user.Roles = roleNames
	return user, nil
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

// Delete refuses to remove the only remaining admin, for the same reason
// roles.Service.Remove refuses to strip just their admin role: someone has
// to be left who can manage users and roles.
func (s *Service) Delete(ctx context.Context, id int64) error {
	isLastAdmin, err := s.roles.IsLastAdmin(ctx, id)
	if err != nil {
		return err
	}
	if isLastAdmin {
		return roles.ErrLastAdmin
	}
	return s.repo.Delete(ctx, id)
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

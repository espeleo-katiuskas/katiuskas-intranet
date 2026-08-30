package users

import (
	"context"
	"time"

	"katiuskas-intranet/backend/internal/roles"
)

type Service struct {
	repo  *Repository
	roles *roles.Service
}

func NewService(repo *Repository, rolesService *roles.Service) *Service {
	return &Service{repo: repo, roles: rolesService}
}

// Create inserts the person, optionally grants them access, then assigns
// any explicitly requested roles as a separate step. There's no longer a
// default "member" role to fall back to — that's automatic now, derived
// from whether the person has an active membership period (which a
// brand-new person doesn't yet). Neither follow-on step is part of the
// same database transaction as the insert — a club-scale app doesn't
// need cross-package transactions for this, but it does mean a failure
// there leaves a person record behind that didn't get everything
// requested.
func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	user, err := s.repo.Create(ctx, in)
	if err != nil {
		return user, err
	}
	if err := s.roles.AssignByName(ctx, user.ID, in.RoleNames); err != nil {
		return user, err
	}
	return s.withRoles(ctx, user)
}

func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return user, err
	}
	return s.withRoles(ctx, user)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	list, err := s.repo.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i], err = s.withRoles(ctx, list[i])
		if err != nil {
			return nil, err
		}
	}
	return list, nil
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (User, error) {
	user, err := s.repo.Update(ctx, id, in)
	if err != nil {
		return user, err
	}
	return s.withRoles(ctx, user)
}

// withRoles overlays the authoritative role list — roles.Service is the
// single place that knows how to compute "member"/"former_member" from
// membership history on top of whatever's actually stored in
// user_roles (see roles.Repository.ListNamesForUser).
func (s *Service) withRoles(ctx context.Context, user User) (User, error) {
	names, err := s.roles.ListNamesForUser(ctx, user.ID)
	if err != nil {
		return user, err
	}
	user.Roles = names
	return user, nil
}

func (s *Service) Directory(ctx context.Context) ([]DirectoryEntry, error) {
	return s.repo.ListDirectory(ctx)
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

func (s *Service) FindActiveByEmailAndPhone(ctx context.Context, email, phone string) (User, error) {
	return s.repo.FindActiveByEmailAndPhone(ctx, email, phone)
}

func (s *Service) GetAccountByUserID(ctx context.Context, userID int64) (Account, error) {
	return s.repo.GetAccountByUserID(ctx, userID)
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

func (s *Service) CreateMagicLinkToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	return s.repo.CreateMagicLinkToken(ctx, userID, tokenHash, expiresAt)
}

func (s *Service) ConsumeMagicLinkToken(ctx context.Context, tokenHash string) (int64, error) {
	return s.repo.ConsumeMagicLinkToken(ctx, tokenHash)
}

func (s *Service) HasRecentMagicLinkToken(ctx context.Context, userID int64, since time.Time) (bool, error) {
	return s.repo.HasRecentMagicLinkToken(ctx, userID, since)
}

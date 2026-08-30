package roles

import (
	"context"
	"errors"
)

var ErrLastAdmin = errors.New("cannot remove the admin role from the only remaining admin")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]Role, error) {
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (Role, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Role, error) {
	return s.repo.Create(ctx, in)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) ListNamesForUser(ctx context.Context, userID int64) ([]string, error) {
	return s.repo.ListNamesForUser(ctx, userID)
}

// AssignByName resolves every name to a role first, so an unknown role
// name — or an automatic one, which can't be granted by hand — fails
// the whole call before any assignment happens.
func (s *Service) AssignByName(ctx context.Context, userID int64, names []string) error {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		role, err := s.repo.GetByName(ctx, name)
		if err != nil {
			return err
		}
		if role.IsAutomatic {
			return ErrAutomaticRole
		}
		ids = append(ids, role.ID)
	}
	for _, id := range ids {
		if err := s.repo.Assign(ctx, userID, id); err != nil {
			return err
		}
	}
	return nil
}

// Assign grants a role by id, refusing an automatic one (member,
// former_member) since those are computed from membership history
// instead — see the package doc comment.
func (s *Service) Assign(ctx context.Context, userID, roleID int64) error {
	role, err := s.repo.GetByID(ctx, roleID)
	if err != nil {
		return err
	}
	if role.IsAutomatic {
		return ErrAutomaticRole
	}
	return s.repo.Assign(ctx, userID, roleID)
}

// Remove refuses to strip an automatic role (nothing to revoke by hand)
// and refuses to strip the "admin" role from the last person who has
// it — otherwise nobody would be left who could manage users or roles.
func (s *Service) Remove(ctx context.Context, userID, roleID int64) error {
	role, err := s.repo.GetByID(ctx, roleID)
	if err != nil {
		return err
	}
	if role.IsAutomatic {
		return ErrAutomaticRole
	}
	if role.Name == "admin" {
		hasIt, err := s.repo.UserHasRoleName(ctx, userID, "admin")
		if err != nil {
			return err
		}
		if hasIt {
			count, err := s.repo.CountUsersWithRoleName(ctx, "admin")
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastAdmin
			}
		}
	}
	return s.repo.Remove(ctx, userID, roleID)
}

// IsLastAdmin reports whether userID is the only person holding the
// "admin" role — used to block deleting that person outright, the same
// way Remove blocks stripping just the role.
func (s *Service) IsLastAdmin(ctx context.Context, userID int64) (bool, error) {
	hasIt, err := s.repo.UserHasRoleName(ctx, userID, "admin")
	if err != nil || !hasIt {
		return false, err
	}
	count, err := s.repo.CountUsersWithRoleName(ctx, "admin")
	if err != nil {
		return false, err
	}
	return count <= 1, nil
}

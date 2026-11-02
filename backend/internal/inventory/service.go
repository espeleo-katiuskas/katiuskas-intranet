package inventory

import (
	"context"
	"time"
)

// RoleLookup says whether a person holds a role, the automatic ones included — roles.Service
// satisfies it structurally (see main.go's wiring).
type RoleLookup interface {
	HasAnyRole(ctx context.Context, userID int64, names ...string) (bool, error)
}

type Service struct {
	repo  *Repository
	roles RoleLookup
}

func NewService(repo *Repository, roles RoleLookup) *Service {
	return &Service{repo: repo, roles: roles}
}

func (s *Service) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	return s.repo.CreateItem(ctx, in)
}

func (s *Service) UpdateItem(ctx context.Context, id int64, in UpdateItemInput) (Item, error) {
	return s.repo.UpdateItem(ctx, id, in)
}

func (s *Service) GetItem(ctx context.Context, id int64) (Item, error) {
	return s.repo.GetItem(ctx, id)
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
	// Who can have material: a loan or a rental, only a member; a sale, a member or a former member.
	if in.UserID != nil {
		switch in.Type {
		case MovementLoan, MovementRental:
			member, err := s.roles.HasAnyRole(ctx, *in.UserID, "member")
			if err != nil {
				return Movement{}, err
			}
			if !member {
				return Movement{}, ErrRecipientNotMember
			}
		case MovementSale:
			ok, err := s.roles.HasAnyRole(ctx, *in.UserID, "member", "former_member")
			if err != nil {
				return Movement{}, err
			}
			if !ok {
				return Movement{}, ErrRecipientNotMemberOrFormer
			}
		}
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

func (s *Service) ListMovementsByActivity(ctx context.Context, activityID int64) ([]Movement, error) {
	return s.repo.ListMovementsByActivity(ctx, activityID)
}

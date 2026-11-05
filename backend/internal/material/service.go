package material

import (
	"context"
	"strings"
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

// blankToNil trims a text and gives nil for a blank one, so that "" is never stored as if it were a value.
// onlyDigits says whether the text is made of digits and nothing else.
func onlyDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func blankToNil(p *string) *string {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// cleanTypeName trims a type name and refuses a blank one.
func cleanTypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankTypeName
	}
	return name, nil
}

func (s *Service) CreateType(ctx context.Context, name string) (MaterialType, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return MaterialType{}, err
	}
	return s.repo.CreateType(ctx, name)
}

func (s *Service) RenameType(ctx context.Context, id int64, name string) (MaterialType, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return MaterialType{}, err
	}
	return s.repo.RenameType(ctx, id, name)
}

func (s *Service) DeleteType(ctx context.Context, id int64) (MaterialType, error) {
	return s.repo.DeleteType(ctx, id)
}

func (s *Service) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	in.Brand, in.Model, in.Code, in.Description = blankToNil(in.Brand), blankToNil(in.Model), blankToNil(in.Code), blankToNil(in.Description)
	if in.Model != nil && in.Brand == nil {
		return Item{}, ErrModelWithoutBrand
	}
	if in.Code != nil && onlyDigits(*in.Code) {
		return Item{}, ErrInvalidItemCode
	}
	return s.repo.CreateItem(ctx, in)
}

func (s *Service) UpdateItem(ctx context.Context, id int64, in UpdateItemInput) (Item, error) {
	in.Brand, in.Model, in.Code, in.Description = blankToNil(in.Brand), blankToNil(in.Model), blankToNil(in.Code), blankToNil(in.Description)
	if in.Model != nil && in.Brand == nil {
		return Item{}, ErrModelWithoutBrand
	}
	if in.Code != nil && onlyDigits(*in.Code) {
		return Item{}, ErrInvalidItemCode
	}
	return s.repo.UpdateItem(ctx, id, in)
}

func (s *Service) DeleteItem(ctx context.Context, id int64) error {
	return s.repo.DeleteItem(ctx, id)
}

func (s *Service) GetItem(ctx context.Context, id int64) (Item, error) {
	return s.repo.GetItem(ctx, id)
}

func (s *Service) ListItems(ctx context.Context) ([]Item, error) {
	return s.repo.ListItems(ctx)
}

func (s *Service) ListMaterialTypes(ctx context.Context) ([]MaterialType, error) {
	return s.repo.ListMaterialTypes(ctx)
}

// DeleteMovement: see Repository.DeleteMovement.
func (s *Service) DeleteMovement(ctx context.Context, id int64) (Movement, error) {
	return s.repo.DeleteMovement(ctx, id)
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

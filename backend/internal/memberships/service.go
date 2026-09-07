package memberships

import (
	"context"
	"time"
)

// MembershipNumberAssigner is implemented by users.Service. memberships
// can't import the users package — users already imports memberships for
// membership dates and on_leave — so this is the narrow interface Create
// needs to give a first-time socio their membership number. Wired in
// after construction (see SetNumberAssigner) since the two services'
// constructors depend on each other.
type MembershipNumberAssigner interface {
	EnsureMembershipNumber(ctx context.Context, userID int64) error
}

type Service struct {
	repo    *Repository
	numbers MembershipNumberAssigner
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// SetNumberAssigner wires in the users.Service that owns membership
// numbers. Left unset, Create still works but skips assigning one — only
// exercised that way by this package's own tests, which don't care about
// numbering; cmd/api and cmd/seed-admin always wire it.
func (s *Service) SetNumberAssigner(numbers MembershipNumberAssigner) {
	s.numbers = numbers
}

// Create starts a membership period and, if the person doesn't already
// have a membership number (someone rejoining keeps the one they got the
// first time — see users.User.MembershipNumber), assigns them the next
// one.
func (s *Service) Create(ctx context.Context, in CreateInput) (Membership, error) {
	m, err := s.repo.Create(ctx, in)
	if err != nil {
		return m, err
	}
	if s.numbers != nil {
		if err := s.numbers.EnsureMembershipNumber(ctx, in.UserID); err != nil {
			return m, err
		}
	}
	return m, nil
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Membership, error) {
	return s.repo.Update(ctx, id, in)
}

// Get is the single-record counterpart to ListByUser/ListActive — used
// by Handler.update to snapshot a membership before applying a PATCH,
// so it can describe exactly what changed in the mutation notification
// afterward.
func (s *Service) Get(ctx context.Context, id int64) (Membership, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) ListActive(ctx context.Context) ([]Membership, error) {
	return s.repo.ListActive(ctx)
}

func (s *Service) WasMemberOn(ctx context.Context, userID int64, date time.Time) (bool, error) {
	return s.repo.WasMemberOn(ctx, userID, date)
}

func (s *Service) IsOnLeave(ctx context.Context, userID int64) (bool, error) {
	return s.repo.IsOnLeave(ctx, userID)
}

func (s *Service) CreateLeave(ctx context.Context, in CreateLeaveInput) (Leave, error) {
	return s.repo.CreateLeave(ctx, in)
}

func (s *Service) UpdateLeave(ctx context.Context, id int64, in UpdateLeaveInput) (Leave, error) {
	return s.repo.UpdateLeave(ctx, id, in)
}

// GetLeave is Get's counterpart for a single leave — used by
// Handler.updateLeave/deleteLeave for the same before-snapshot reason.
func (s *Service) GetLeave(ctx context.Context, id int64) (Leave, error) {
	return s.repo.GetLeaveByID(ctx, id)
}

func (s *Service) DeleteLeave(ctx context.Context, id int64) error {
	return s.repo.DeleteLeave(ctx, id)
}

func (s *Service) ListLeavesByMembership(ctx context.Context, membershipID int64) ([]Leave, error) {
	return s.repo.ListLeavesByMembership(ctx, membershipID)
}

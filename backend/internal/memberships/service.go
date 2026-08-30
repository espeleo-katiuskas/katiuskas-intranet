package memberships

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Membership, error) {
	return s.repo.Create(ctx, in)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Membership, error) {
	return s.repo.Update(ctx, id, in)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) ListActive(ctx context.Context) ([]Membership, error) {
	return s.repo.ListActive(ctx)
}

func (s *Service) IsActiveMember(ctx context.Context, userID int64) (bool, error) {
	return s.repo.IsActiveMember(ctx, userID)
}

func (s *Service) IsOnLeave(ctx context.Context, userID int64) (bool, error) {
	return s.repo.IsOnLeave(ctx, userID)
}

// LatestByUser returns the person's most recent membership period —
// active if they currently are one, otherwise their last one — or nil if
// they've never been a member. Used to show a start/end "alta/baja" date
// on the people list without a separate per-person round trip for full
// history.
func (s *Service) LatestByUser(ctx context.Context, userID int64) (*Membership, error) {
	list, err := s.repo.ListByUser(ctx, userID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (s *Service) CreateLeave(ctx context.Context, in CreateLeaveInput) (Leave, error) {
	return s.repo.CreateLeave(ctx, in)
}

func (s *Service) UpdateLeave(ctx context.Context, id int64, in UpdateLeaveInput) (Leave, error) {
	return s.repo.UpdateLeave(ctx, id, in)
}

func (s *Service) DeleteLeave(ctx context.Context, id int64) error {
	return s.repo.DeleteLeave(ctx, id)
}

func (s *Service) ListLeavesByMembership(ctx context.Context, membershipID int64) ([]Leave, error) {
	return s.repo.ListLeavesByMembership(ctx, membershipID)
}

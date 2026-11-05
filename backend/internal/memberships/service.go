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
// by Handler.update to snapshot a membership before applying a PUT,
// so it can describe exactly what changed in the mutation notification
// afterward.
func (s *Service) Get(ctx context.Context, id int64) (Membership, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Membership, error) {
	return s.repo.ListByUser(ctx, userID)
}

// ListByUsers, OnLeaveAmong and LatestDues are ListByUser, IsOnLeave and
// LatestDue for a whole list of people in one query each, for listings that
// would otherwise ask once per person.
func (s *Service) ListByUsers(ctx context.Context, userIDs []int64) (map[int64][]Membership, error) {
	return s.repo.ListByUsers(ctx, userIDs)
}

func (s *Service) OnLeaveAmong(ctx context.Context, userIDs []int64) (map[int64]bool, error) {
	return s.repo.OnLeaveAmong(ctx, userIDs)
}

func (s *Service) LatestDues(ctx context.Context, userIDs []int64) (map[int64]*Due, error) {
	return s.repo.LatestDuesForUsers(ctx, userIDs)
}

func (s *Service) ListActive(ctx context.Context) ([]Membership, error) {
	return s.repo.ListActive(ctx)
}

func (s *Service) WasMemberDuringYear(ctx context.Context, userID int64, year int) (bool, error) {
	return s.repo.WasMemberDuringYear(ctx, userID, year)
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

// GetDue and DeleteDue: see Repository.DeleteDue.
func (s *Service) GetDue(ctx context.Context, id int64) (Due, error) {
	return s.repo.GetDueByID(ctx, id)
}

func (s *Service) DeleteDue(ctx context.Context, id int64) error {
	return s.repo.DeleteDue(ctx, id)
}

func (s *Service) DeleteLeave(ctx context.Context, id int64) error {
	return s.repo.DeleteLeave(ctx, id)
}

func (s *Service) ListLeavesByUser(ctx context.Context, userID int64) ([]Leave, error) {
	return s.repo.ListLeavesByUser(ctx, userID)
}

// RecordDue records userID's cuota for year, tied to transactionID — the
// payment that recorded it (see Due's own doc comment). If userID has no
// membership period on file at all yet, this is their very first due —
// the matrícula alongside the current year's cuota — so a new membership
// period is started for them first, occurredOn as its StartDate, the
// same "alta" a person otherwise gets through Create/POST
// /users/{login}/memberships; someone with a prior, since-closed
// membership who's rejoining isn't touched by this at all (HasAnyMembership
// is true for them already), leaving that case to the existing, separate
// alta flow instead of guessing whether a new period should open here too.
// occurredOn is therefore optional (the zero time) for anyone who already
// has a membership on file; without one on file, ErrStartDateRequired.
//
// The transaction is checked first — it has to exist (ErrUnknownTransaction)
// and have an entry on an account flagged allows_dues
// (ErrTransactionNotForDues) — so that a rejected due can't have already
// started a membership period for someone as a side effect.
func (s *Service) RecordDue(ctx context.Context, userID int64, year int, transactionID int64, occurredOn time.Time) (Due, error) {
	exists, allowed, err := s.repo.TransactionAllowsDues(ctx, transactionID)
	if err != nil {
		return Due{}, err
	}
	if !exists {
		return Due{}, ErrUnknownTransaction
	}
	if !allowed {
		return Due{}, ErrTransactionNotForDues
	}
	hasAny, err := s.repo.HasAnyMembership(ctx, userID)
	if err != nil {
		return Due{}, err
	}
	if !hasAny {
		// occurredOn is only needed here, to start their first period; a
		// zero value means the caller didn't give one.
		if occurredOn.IsZero() {
			return Due{}, ErrStartDateRequired
		}
		if _, err := s.Create(ctx, CreateInput{UserID: userID, StartDate: occurredOn}); err != nil {
			return Due{}, err
		}
	}
	return s.repo.CreateDue(ctx, CreateDueInput{UserID: userID, Year: year, TransactionID: transactionID})
}

// LatestDue is the due for the latest year this person has paid, nil if
// none — see Repository.LatestDueForUser.
func (s *Service) LatestDue(ctx context.Context, userID int64) (*Due, error) {
	return s.repo.LatestDueForUser(ctx, userID)
}

func (s *Service) ListDuesForUser(ctx context.Context, userID int64) ([]Due, error) {
	return s.repo.ListDuesForUser(ctx, userID)
}

// ListDuesForTransaction returns every due recorded against the
// transaction. An unknown transaction id just comes back an empty list,
// the same as one that paid for no dues — nothing here needs to tell the
// two apart.
func (s *Service) ListDuesForTransaction(ctx context.Context, transactionID int64) ([]Due, error) {
	return s.repo.ListDuesForTransaction(ctx, transactionID)
}

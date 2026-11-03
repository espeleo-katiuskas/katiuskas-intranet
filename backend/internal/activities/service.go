package activities

import (
	"context"
	"errors"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
)

// ErrNotMember means one of the people to sign up is not a member (a socio) now: anybody who is
// can join any activity, whatever its type and whether or not they are federated, and only them.
var ErrNotMember = errors.New("only a member can join an activity")

// RoleLookup says whether a person holds a role, the automatic ones included — roles.Service
// satisfies it structurally (see main.go's wiring).
type RoleLookup interface {
	HasAnyRole(ctx context.Context, userID int64, names ...string) (bool, error)
}

type Service struct {
	repo     *Repository
	licenses *licenses.Service
	roles    RoleLookup
}

func NewService(repo *Repository, licensesService *licenses.Service, roles RoleLookup) *Service {
	return &Service{repo: repo, licenses: licensesService, roles: roles}
}

// checkMembers verifies that every given user is a member now. Called both when an activity is
// created with initial participants and when someone is signed up afterwards.
func (s *Service) checkMembers(ctx context.Context, userIDs []int64) error {
	for _, userID := range userIDs {
		member, err := s.roles.HasAnyRole(ctx, userID, "member")
		if err != nil {
			return err
		}
		if !member {
			return ErrNotMember
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Activity, error) {
	if !in.Type.Valid() {
		return Activity{}, ErrInvalidType
	}
	if !in.Visibility.Valid() {
		return Activity{}, ErrInvalidVisibility
	}
	if in.MaxParticipants != nil && *in.MaxParticipants < 1 {
		return Activity{}, ErrInvalidMaxParticipants
	}
	if err := s.checkMembers(ctx, in.ParticipantUserIDs); err != nil {
		return Activity{}, err
	}
	return s.repo.Create(ctx, in)
}

// clampOffset defaults a negative offset (a malformed or hand-edited
// query string) to the start instead of passing it through to
// Postgres's own OFFSET clause, which rejects a negative value outright
// — a raw, unhandled 500 otherwise, the same reasoning limit above
// already gets for an out-of-range value.
func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func (s *Service) List(ctx context.Context, limit, offset int, ascending bool) ([]Activity, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.List(ctx, limit, clampOffset(offset), ascending)
}

func (s *Service) ListActive(ctx context.Context, limit, offset int, ascending bool) ([]Activity, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListActive(ctx, limit, clampOffset(offset), ascending)
}

func (s *Service) Get(ctx context.Context, id int64) (Activity, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Activity, error) {
	if in.Type != nil && !in.Type.Valid() {
		return Activity{}, ErrInvalidType
	}
	if in.Visibility != nil && !in.Visibility.Valid() {
		return Activity{}, ErrInvalidVisibility
	}
	return s.repo.Update(ctx, id, in)
}

// Delete removes an activity with nothing attached (see Repository.Delete).
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// Join signs userID up to the activity; addedBy is who is doing it (userID themselves, or somebody else).
func (s *Service) Join(ctx context.Context, activityID, userID, addedBy int64) error {
	if _, err := s.repo.GetByID(ctx, activityID); err != nil {
		return err
	}
	if err := s.checkMembers(ctx, []int64{userID}); err != nil {
		return err
	}
	return s.repo.AddParticipant(ctx, activityID, userID, addedBy)
}

// AddedBy is who signed userID up to the activity, and false if they are not signed up.
func (s *Service) AddedBy(ctx context.Context, activityID, userID int64) (int64, bool, error) {
	return s.repo.AddedBy(ctx, activityID, userID)
}

func (s *Service) Leave(ctx context.Context, activityID, userID int64) error {
	return s.repo.RemoveParticipant(ctx, activityID, userID)
}

// ListParticipants lists who is signed up, each saying whether they are federated this year.
func (s *Service) ListParticipants(ctx context.Context, activityID int64) ([]Participant, error) {
	list, err := s.repo.ListParticipants(ctx, activityID)
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]int64, len(list))
	for i, p := range list {
		ids[i] = p.UserID
	}
	federated, err := s.licenses.CurrentByUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		_, list[i].Federated = federated[list[i].UserID]
	}
	return list, nil
}

func (s *Service) IsParticipant(ctx context.Context, activityID, userID int64) (bool, error) {
	return s.repo.IsParticipant(ctx, activityID, userID)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Activity, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *Service) ListHolidays(ctx context.Context, year int) ([]Holiday, error) {
	return s.repo.ListHolidays(ctx, year)
}

func (s *Service) CreateHoliday(ctx context.Context, date time.Time, name string) (Holiday, error) {
	return s.repo.CreateHoliday(ctx, date, name)
}

func (s *Service) DeleteHoliday(ctx context.Context, id int64) (Holiday, error) {
	return s.repo.DeleteHoliday(ctx, id)
}

package activities

import (
	"context"
	"errors"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
)

// ErrNotFederated means one of the given participants doesn't currently
// hold a valid federative license, required to join a caving or
// canyoning activity (see Type.RequiresFederation).
var ErrNotFederated = errors.New("only a currently federated member can join a caving or canyoning activity")

type Service struct {
	repo     *Repository
	licenses *licenses.Service
}

func NewService(repo *Repository, licensesService *licenses.Service) *Service {
	return &Service{repo: repo, licenses: licensesService}
}

// checkFederationRequired verifies that every given user is currently
// federated, if the activity type requires it. Called both when an
// activity is created with initial participants and when someone joins
// one afterwards.
func (s *Service) checkFederationRequired(ctx context.Context, t Type, userIDs []int64) error {
	if !t.RequiresFederation() {
		return nil
	}
	for _, userID := range userIDs {
		federated, err := s.licenses.IsCurrentlyFederated(ctx, userID)
		if err != nil {
			return err
		}
		if !federated {
			return ErrNotFederated
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Activity, error) {
	if err := s.checkFederationRequired(ctx, in.Type, in.ParticipantUserIDs); err != nil {
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
	return s.repo.Update(ctx, id, in)
}

func (s *Service) Cancel(ctx context.Context, id int64) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Join(ctx context.Context, activityID, userID int64) error {
	activity, err := s.repo.GetByID(ctx, activityID)
	if err != nil {
		return err
	}
	if err := s.checkFederationRequired(ctx, activity.Type, []int64{userID}); err != nil {
		return err
	}
	return s.repo.AddParticipant(ctx, activityID, userID)
}

func (s *Service) Leave(ctx context.Context, activityID, userID int64) error {
	return s.repo.RemoveParticipant(ctx, activityID, userID)
}

func (s *Service) ListParticipants(ctx context.Context, activityID int64) ([]Participant, error) {
	return s.repo.ListParticipants(ctx, activityID)
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

package activities

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/clubtime"
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
	places   PlaceLookup
	refs     PlaceReferences
}

func NewService(repo *Repository, licensesService *licenses.Service, roles RoleLookup, places PlaceLookup) *Service {
	return &Service{repo: repo, licenses: licensesService, roles: roles, places: places}
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
	placeIDs, err := cleanPlaceIDs(in.PlaceIDs)
	if err != nil {
		return Activity{}, err
	}
	if err := s.checkPlacesExist(ctx, placeIDs); err != nil {
		return Activity{}, err
	}
	in.PlaceIDs = placeIDs
	a, err := s.repo.Create(ctx, in)
	if err != nil {
		return Activity{}, err
	}
	s.refer(ctx, a.ID, placeIDs)
	return a, nil
}

// cleanPlaceIDs drops repeated ids and refuses too many.
func cleanPlaceIDs(ids []int64) ([]int64, error) {
	seen := make(map[int64]bool, len(ids))
	clean := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if len(clean) > MaxPlaces {
		return nil, ErrTooManyPlaces
	}
	return clean, nil
}

// ListPlaces are the places an activity goes to.
func (s *Service) ListPlaces(ctx context.Context, activityID int64) ([]PlaceRef, error) {
	if _, err := s.repo.GetByID(ctx, activityID); err != nil {
		return nil, err
	}
	ids, err := s.repo.ListPlaceIDs(ctx, activityID)
	if err != nil {
		return nil, err
	}
	return s.places.PlaceRefs(ctx, ids)
}

// SetPlaces makes the places of an activity exactly the given ones (none unlinks them all). It answers
// with the places before and after.
func (s *Service) SetPlaces(ctx context.Context, activityID int64, placeIDs []int64) (before, after []PlaceRef, err error) {
	if placeIDs, err = cleanPlaceIDs(placeIDs); err != nil {
		return nil, nil, err
	}
	// Only the places that are new have to exist: one that is already there stays even if the catalogue
	// has since closed it to the person who edits.
	current, err := s.repo.ListPlaceIDs(ctx, activityID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkPlacesExist(ctx, without(placeIDs, current)); err != nil {
		return nil, nil, err
	}
	// The catalogue is told first of what is added, so that a place cannot be deleted between the two.
	s.refer(ctx, activityID, without(placeIDs, current))
	beforeIDs, afterIDs, err := s.repo.SetPlaces(ctx, activityID, placeIDs)
	if err != nil {
		return nil, nil, err
	}
	s.unrefer(ctx, activityID, without(beforeIDs, afterIDs))
	if before, err = s.places.PlaceRefs(ctx, beforeIDs); err != nil {
		return nil, nil, err
	}
	if after, err = s.places.PlaceRefs(ctx, afterIDs); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// VisitedBy is the places a person has been to, the most recently visited first: those of the activities they
// organized or took part in that have started (today, in the club's time zone, counts) and were not cancelled,
// each with how many such activities went there and when the last one ended. The catalogue says what each
// place is called, what it is and where.
func (s *Service) VisitedBy(ctx context.Context, userID int64) ([]VisitedPlace, error) {
	visits, err := s.repo.PlaceVisitsOf(ctx, userID, clubtime.Today())
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(visits))
	for i, v := range visits {
		ids[i] = v.PlaceID
	}
	refs, err := s.places.PlaceRefs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]PlaceRef, len(refs))
	for _, ref := range refs {
		byID[ref.ID] = ref
	}
	list := make([]VisitedPlace, 0, len(visits))
	for _, v := range visits {
		ref, ok := byID[v.PlaceID]
		if !ok {
			continue
		}
		list = append(list, VisitedPlace{ID: ref.ID, TypeName: ref.TypeName, Name: ref.Name, Where: ref.Where, Visits: v.Visits, LastVisit: v.LastVisit})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].LastVisit.Equal(list[j].LastVisit) {
			return list[i].LastVisit.After(list[j].LastVisit)
		}
		if a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name); a != b {
			return a < b
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// ListByPlace are the activities that went to a place, the latest first; a place that is not there is
// ErrUnknownPlace.
func (s *Service) ListByPlace(ctx context.Context, placeID int64) ([]Activity, error) {
	found, err := s.places.PlaceRefs(ctx, []int64{placeID})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrUnknownPlace
	}
	return s.repo.ListByPlace(ctx, placeID)
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
	placeIDs, err := s.repo.ListPlaceIDs(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.unrefer(ctx, id, placeIDs)
	return nil
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

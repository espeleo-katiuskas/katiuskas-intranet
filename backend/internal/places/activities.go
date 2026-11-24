package places

import (
	"context"
	"sort"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/placelinks"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/clubtime"
)

// ActivityLookup is what the catalogue asks of the club's activities, and nothing else: the catalogue
// does not read their tables, and an activity only names its places by id (activities.Service is the
// implementation; the activities ask the catalogue back through activities.PlaceLookup).
type ActivityLookup interface {
	// ActivityCounts is, for each place, in how many activities that are not cancelled it is one of the
	// places they go to; a place with none is not in the answer.
	ActivityCounts(ctx context.Context, placeIDs []int64) (map[int64]int, error)
	// HasActivities says whether any activity, cancelled or not, goes or went to the place.
	HasActivities(ctx context.Context, placeID int64) (bool, error)
	// VisitsOf is the places a person has been to: those of the activities they organized or took part in
	// that started on or before today ("2026-11-18") and were not cancelled, in no order.
	VisitsOf(ctx context.Context, userID int64, today string) ([]placelinks.PlaceVisit, error)
}

// SetActivities gives the service its source of activities. It is a setter, and not an argument of
// NewService, because the activities need the catalogue to name their places and each is made with the
// other in hand; it must be called before the service is used.
func (s *Service) SetActivities(activities ActivityLookup) {
	s.activities = activities
}

// countActivities fills in ActivityCount of the places, with one question for all of them.
func (s *Service) countActivities(ctx context.Context, items ...*ListItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	counts, err := s.activities.ActivityCounts(ctx, ids)
	if err != nil {
		return err
	}
	for _, it := range items {
		it.ActivityCount = counts[it.ID]
	}
	return nil
}

// VisitedBy is the places a person has been to, the most recently visited first: those of the activities
// they organized or took part in that have started (today, in the club's time zone, counts) and were not
// cancelled, each with how many such activities went there and when the last one ended.
func (s *Service) VisitedBy(ctx context.Context, userID int64) ([]VisitedPlace, error) {
	visits, err := s.activities.VisitsOf(ctx, userID, clubtime.Today())
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(visits))
	for i, v := range visits {
		ids[i] = v.PlaceID
	}
	found, err := s.repo.VisitedPlacesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]VisitedPlace, len(found))
	for _, p := range found {
		byID[p.ID] = p
	}
	list := make([]VisitedPlace, 0, len(visits))
	for _, v := range visits {
		p, ok := byID[v.PlaceID]
		if !ok {
			continue
		}
		p.Visits, p.LastVisit = v.Visits, v.LastVisit
		list = append(list, p)
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

// PlaceRefs names the places with those ids, by name, for whoever refers to places by id (the
// activities: activities.PlaceLookup). An id that is no place is left out.
func (s *Service) PlaceRefs(ctx context.Context, ids []int64) ([]placelinks.PlaceRef, error) {
	if len(ids) == 0 {
		return []placelinks.PlaceRef{}, nil
	}
	return s.repo.PlaceRefs(ctx, ids)
}

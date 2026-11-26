package activities

import (
	"context"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/placelinks"
)

// PlaceLookup is what the activities need of the catalogue of places: to name the places an activity
// goes to and to know that one exists. An activity keeps only the ids of its places (activity_places);
// it never reads the catalogue's tables. The ids that name no place are left out, and the places come
// by name.
type PlaceLookup interface {
	PlaceRefs(ctx context.Context, ids []int64) ([]placelinks.PlaceRef, error)
}

// What the activities tell the catalogue (places.ActivityLookup, which this Service satisfies). The
// catalogue does not read the activities: it asks.

// ActivityCounts is, for each of the places, in how many activities that are not cancelled it is one
// of the places they go to; a place with none is not in the answer.
func (s *Service) ActivityCounts(ctx context.Context, placeIDs []int64) (map[int64]int, error) {
	if len(placeIDs) == 0 {
		return map[int64]int{}, nil
	}
	return s.repo.PlaceActivityCounts(ctx, placeIDs)
}

// HasActivities says whether any activity, cancelled or not, goes or went to the place.
func (s *Service) HasActivities(ctx context.Context, placeID int64) (bool, error) {
	return s.repo.PlaceHasActivities(ctx, placeID)
}

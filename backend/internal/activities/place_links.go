package activities

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

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

// PlaceReferences is how the club tells the catalogue which places it uses, when the catalogue is not part
// of this program: each link of an activity to a place is a reference with the key "activity:<id>", and a
// place that has any is not deleted. Both calls are idempotent. catalogueclient.Client satisfies it.
type PlaceReferences interface {
	Refer(ctx context.Context, placeID int64, key string) error
	Unrefer(ctx context.Context, placeID int64, key string) error
}

// WithPlaceReferences makes the service tell the catalogue of the places its activities go to. Without it
// (the catalogue is in this program, which asks the activities itself: places.ActivityLookup) nothing is
// told.
func (s *Service) WithPlaceReferences(refs PlaceReferences) *Service {
	s.refs = refs
	return s
}

// ReferenceKey is the key of the reference that an activity makes to each of its places.
func ReferenceKey(activityID int64) string {
	return "activity:" + strconv.FormatInt(activityID, 10)
}

// refer and unrefer tell the catalogue after the activity has changed, and a failure is logged and not
// returned: the change is made and the person must not be told it was not. A reference that is missing only
// means that the place could be deleted by mistake, and one that is left over only that it cannot; SyncReferences
// (cmd/syncrefs) puts the missing ones back.
func (s *Service) refer(ctx context.Context, activityID int64, placeIDs []int64) {
	if s.refs == nil {
		return
	}
	for _, id := range placeIDs {
		if err := s.refs.Refer(ctx, id, ReferenceKey(activityID)); err != nil {
			slog.Error("tell the catalogue that an activity goes to a place", "activity", activityID, "place", id, "error", err)
		}
	}
}

func (s *Service) unrefer(ctx context.Context, activityID int64, placeIDs []int64) {
	if s.refs == nil {
		return
	}
	for _, id := range placeIDs {
		if err := s.refs.Unrefer(ctx, id, ReferenceKey(activityID)); err != nil {
			slog.Error("tell the catalogue that an activity no longer goes to a place", "activity", activityID, "place", id, "error", err)
		}
	}
}

// SyncReferences tells the catalogue of every link of an activity to a place, for what went wrong when it
// was told one by one (it was not answering). It cannot find the references that are left over.
func (s *Service) SyncReferences(ctx context.Context) (int, error) {
	if s.refs == nil {
		return 0, errors.New("no catalogue to tell")
	}
	links, err := s.repo.ListAllPlaceLinks(ctx)
	if err != nil {
		return 0, err
	}
	for i, l := range links {
		if err := s.refs.Refer(ctx, l.PlaceID, ReferenceKey(l.ActivityID)); err != nil {
			return i, fmt.Errorf("activity %d, place %d: %w", l.ActivityID, l.PlaceID, err)
		}
	}
	return len(links), nil
}

// checkPlacesExist is ErrUnknownPlace if the catalogue does not know one of the places (or does not let the
// person who asks know it). The activities keep only ids, so nothing in the database says it any more.
func (s *Service) checkPlacesExist(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := s.places.PlaceRefs(ctx, ids)
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(found))
	for _, p := range found {
		known[p.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return ErrUnknownPlace
		}
	}
	return nil
}

// without is the ids of a that are not in b.
func without(a, b []int64) []int64 {
	in := make(map[int64]bool, len(b))
	for _, id := range b {
		in[id] = true
	}
	var out []int64
	for _, id := range a {
		if !in[id] {
			out = append(out, id)
		}
	}
	return out
}

// PlaceLink is the link of an activity to a place.
type PlaceLink struct{ ActivityID, PlaceID int64 }

package places

import (
	"encoding/json"
	"fmt"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
)

// What a place keeps of the OpenStreetMap element it is, to tell what changed there: the tags and the point of
// the element at the time the place was linked to it or somebody last looked at what had changed (the column
// `osm_seen`). A load of the mirror changes the element, never the place, so what differs is told to a person.

type osmSnapshot struct {
	Tags map[string]string `json:"tags"`
	Lat  float64           `json:"lat"`
	Lon  float64           `json:"lon"`
}

// OsmChange is one thing that differs: the key of the tag (or "position", or "removed", when the element is
// no longer in OpenStreetMap), what it was and what it is. Before or After is absent when the tag was not
// there or is not any more.
type OsmChange struct {
	Key    string  `json:"key"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
}

// osmWatched are the tags whose change is worth telling: what the place shows or can copy (the names, the
// altitude, the description, the website, the facts of a hut or a canyon) and what says what the element is.
// The rest (who surveyed it, when, the source…) changes all the time and says nothing to the club.
var osmWatched = []string{
	"name", "name:es", "alt_name", "ele", "description", "website", "access", "fee", "capacity", "beds",
	"operator", "max:abseil", "rating", "depth", "length",
	"natural", "tourism", "amenity", "shelter_type", "sport", "route", "highway",
}

// osmMovedM is how far the point of the element has to move (metres) to be told.
const osmMovedM = 10

func osmText(lat, lon float64) string { return fmt.Sprintf("%.5f, %.5f", lat, lon) }

// osmChanges compares the element as it was seen with how it is now (`gone` when the mirror has it marked as no
// longer in OpenStreetMap). A place with nothing seen has nothing to compare, so it has no changes.
func osmChanges(seen []byte, now osmSnapshot, gone bool) []OsmChange {
	if len(seen) == 0 {
		return nil
	}
	var before osmSnapshot
	if err := json.Unmarshal(seen, &before); err != nil {
		return nil
	}
	var changes []OsmChange
	for _, key := range osmWatched {
		b, hadB := before.Tags[key]
		a, hasA := now.Tags[key]
		if hadB == hasA && b == a {
			continue
		}
		c := OsmChange{Key: key}
		if hadB {
			c.Before = &b
		}
		if hasA {
			c.After = &a
		}
		changes = append(changes, c)
	}
	if osmmirror.DistanceM(before.Lat, before.Lon, now.Lat, now.Lon) > osmMovedM {
		b, a := osmText(before.Lat, before.Lon), osmText(now.Lat, now.Lon)
		changes = append(changes, OsmChange{Key: "position", Before: &b, After: &a})
	}
	if gone {
		changes = append(changes, OsmChange{Key: "removed"})
	}
	return changes
}

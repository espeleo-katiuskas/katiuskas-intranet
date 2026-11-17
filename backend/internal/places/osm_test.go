package places_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

func mirror(t *testing.T, e *env, ids ...int64) *osmmirror.Repository {
	t.Helper()
	repo := osmmirror.NewRepository(e.tx)
	var elements []osmmirror.Element
	for _, id := range ids {
		elements = append(elements, osmmirror.Element{
			Type: "node", ID: id, Version: 1, Timestamp: time.Unix(1700000000, 0).UTC(), Category: "cave",
			Lat: 40.5, Lon: -3.7, Tags: map[string]string{"natural": "cave_entrance", "name": "Cueva"},
		})
	}
	if _, err := repo.Apply(context.Background(), "spain", elements); err != nil {
		t.Fatal(err)
	}
	return repo
}

// A place can be made from an element of the OpenStreetMap mirror: it keeps which one, an element is at
// most one place, an edit leaves the link as it was, and the element stops being offered on the map.
func TestPlaceFromOsmElement(t *testing.T) {
	e := newEnv(t)
	repo := mirror(t, e, 1, 2)
	ctx := context.Background()
	bounds := osmmirror.Bounds{South: 40, West: -4, North: 41, East: -3}

	before, _, err := repo.InBounds(ctx, bounds, osmmirror.Categories, 100)
	if err != nil || len(before) != 2 {
		t.Fatalf("two candidates expected: %+v %v", before, err)
	}

	p := e.newPlace(e.cavidad, "Cueva", map[string]any{"osm_type": "node", "osm_id": 1})
	if p.OsmType == nil || *p.OsmType != "node" || p.OsmID == nil || *p.OsmID != 1 {
		t.Fatalf("the place keeps its element: %+v %+v", p.OsmType, p.OsmID)
	}
	got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	if got.OsmID == nil || *got.OsmID != 1 {
		t.Errorf("GET gives the element too: %+v", got)
	}
	e.notifier.wait(t)

	after, _, err := repo.InBounds(ctx, bounds, osmmirror.Categories, 100)
	if err != nil || len(after) != 1 || after[0].ID != 2 {
		t.Errorf("an element that is a place is no longer a candidate: %+v %v", after, err)
	}

	// Half of the pair, or a type that is not one, is refused before the database is asked.
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token,
		body(e.cavidad, "Otra", map[string]any{"osm_type": "node"})), 400, "unknown_osm_element")
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token,
		body(e.cavidad, "Otra", map[string]any{"osm_id": 2})), 400, "unknown_osm_element")
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token,
		body(e.cavidad, "Otra", map[string]any{"osm_type": "road", "osm_id": 2})), 400, "unknown_osm_element")

	// An edit does not read the element: it neither changes nor drops it.
	rec := e.json("PUT", placePath(p, ""), e.member.token,
		body(e.cavidad, "Cueva renombrada", map[string]any{"osm_type": "node", "osm_id": 2}))
	status(t, rec, http.StatusOK)
	edited := decode[places.Place](t, rec)
	if edited.Name != "Cueva renombrada" || edited.OsmID == nil || *edited.OsmID != 1 {
		t.Errorf("the element stays after an edit: %+v", edited)
	}
	e.notifier.wait(t)

	// A place with no element, as always.
	plain := e.newPlace(e.cavidad, "Sin OSM", nil)
	if plain.OsmType != nil || plain.OsmID != nil {
		t.Errorf("no element expected: %+v %+v", plain.OsmType, plain.OsmID)
	}
}

// Each of these ends a database transaction with an error, so each has its own.
func TestPlaceOsmElementInUse(t *testing.T) {
	e := newEnv(t)
	mirror(t, e, 1)
	e.newPlace(e.cavidad, "Cueva", map[string]any{"osm_type": "node", "osm_id": 1})
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token,
		body(e.cavidad, "Otra", map[string]any{"osm_type": "node", "osm_id": 1})), 409, "osm_element_in_use")
}

func TestPlaceUnknownOsmElement(t *testing.T) {
	e := newEnv(t)
	mirror(t, e, 1)
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token,
		body(e.cavidad, "Otra", map[string]any{"osm_type": "node", "osm_id": 99})), 400, "unknown_osm_element")
}

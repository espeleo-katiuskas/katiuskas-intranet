package places_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A place that already exists is linked to an element of the mirror, linked to another instead, and
// unlinked; the element stops being a candidate while it is linked and is one again after.
func TestLinkAndUnlinkOsmElement(t *testing.T) {
	e := newEnv(t)
	repo := mirror(t, e, 1, 2)
	service := osmmirror.NewService(repo)
	ctx := context.Background()
	candidates := func() []int64 {
		found, err := service.Nearby(ctx, 40.5, -3.7, 500, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, c := range found {
			ids = append(ids, c.ID)
		}
		return ids
	}
	p := e.newPlace(e.cavidad, "Cueva", nil)
	e.notifier.wait(t)
	link := func(body map[string]any) *httptest.ResponseRecorder {
		return e.json("PUT", placePath(p, "/links/osm"), e.member.token, body)
	}

	rec := link(map[string]any{"osm_type": "node", "osm_id": 1})
	status(t, rec, http.StatusOK)
	got := decode[places.Place](t, rec)
	if got.OsmType == nil || *got.OsmType != "node" || got.OsmID == nil || *got.OsmID != 1 || got.Name != "Cueva" ||
		got.UpdatedBy != e.member.user.Login {
		t.Errorf("unexpected place: %+v %+v", got.OsmType, got.OsmID)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha vinculado el lugar «Cueva»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	if ids := candidates(); len(ids) != 1 || ids[0] != 2 {
		t.Errorf("the linked element is no candidate: %v", ids)
	}

	// Linking to another replaces the link: the first is a candidate again.
	status(t, link(map[string]any{"osm_type": "node", "osm_id": 2}), http.StatusOK)
	e.notifier.wait(t)
	if ids := candidates(); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("the element that was left is a candidate again: %v", ids)
	}

	// Half of the pair, a bad type and an id of nothing are refused before the database is asked.
	errorCode(t, link(map[string]any{"osm_type": "node"}), 400, "unknown_osm_element")
	errorCode(t, link(map[string]any{"osm_id": 1}), 400, "unknown_osm_element")
	errorCode(t, link(map[string]any{"osm_type": "road", "osm_id": 1}), 400, "unknown_osm_element")
	errorCode(t, e.json("PUT", "/api/v1/places/999999/links/osm", e.member.token, map[string]any{"osm_type": "node", "osm_id": 1}), 404, "not_found")

	// Unlinking takes it away; doing it again is no error.
	rec = e.json("DELETE", placePath(p, "/links/osm"), e.member.token, nil)
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.OsmType != nil || got.OsmID != nil {
		t.Errorf("no element expected: %+v %+v", got.OsmType, got.OsmID)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha desvinculado el lugar «Cueva»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	status(t, e.json("DELETE", placePath(p, "/links/osm"), e.member.token, nil), http.StatusOK)
	e.notifier.wait(t)
	if ids := candidates(); len(ids) != 2 {
		t.Errorf("both are candidates again: %v", ids)
	}
	errorCode(t, e.json("DELETE", "/api/v1/places/999999/links/osm", e.member.token, nil), 404, "not_found")
	if rec := e.json("PUT", placePath(p, "/links/osm"), "", map[string]any{"osm_type": "node", "osm_id": 1}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

// Each of these ends a database transaction with an error, so each has its own.
func TestLinkOsmElementInUseByAnotherPlace(t *testing.T) {
	e := newEnv(t)
	mirror(t, e, 1)
	e.newPlace(e.cavidad, "Una", map[string]any{"osm_type": "node", "osm_id": 1})
	other := e.newPlace(e.cavidad, "Otra", nil)
	errorCode(t, e.json("PUT", placePath(other, "/links/osm"), e.member.token, map[string]any{"osm_type": "node", "osm_id": 1}), 409, "osm_element_in_use")
}

func TestLinkUnknownOsmElement(t *testing.T) {
	e := newEnv(t)
	mirror(t, e, 1)
	p := e.newPlace(e.cavidad, "Una", nil)
	errorCode(t, e.json("PUT", placePath(p, "/links/osm"), e.member.token, map[string]any{"osm_type": "node", "osm_id": 99}), 400, "unknown_osm_element")
}

// What changes in the element of a place, when a load of the mirror changes it, is told by the place until a
// person says it has looked: for the tags that matter, a move, and the element going; and the list says which
// places have changes.
func TestOsmChangesOfAPlace(t *testing.T) {
	e := newEnv(t)
	// Two elements: the SQL that takes the element of a place must pick only its own (with one it would pass
	// whatever it picked).
	repo := mirror(t, e, 1, 2)
	ctx := context.Background()
	p := e.newPlace(e.cavidad, "Cueva", map[string]any{"osm_type": "node", "osm_id": 1})
	e.notifier.wait(t)
	plain := e.newPlace(e.cavidad, "Sin OSM", nil)
	e.notifier.wait(t)
	_ = plain

	get := func() places.Place {
		return decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	}
	if got := get(); got.OsmChanged || len(got.OsmChanges) != 0 {
		t.Fatalf("nothing changed yet: %+v", got.OsmChanges)
	}

	// A load changes the name, adds an altitude and moves the point 20 m; a tag nobody cares about changes too.
	changed := osmmirror.Element{
		Type: "node", ID: 1, Version: 2, Timestamp: time.Unix(1800000000, 0).UTC(), Category: "cave",
		Lat: 40.5002, Lon: -3.7,
		Tags: map[string]string{"natural": "cave_entrance", "name": "Cueva Nueva", "ele": "1300", "survey:date": "2026-11-17"},
	}
	if _, err := repo.Apply(ctx, "spain", []osmmirror.Element{changed}); err != nil {
		t.Fatal(err)
	}
	got := get()
	keys := map[string]places.OsmChange{}
	for _, c := range got.OsmChanges {
		keys[c.Key] = c
	}
	if !got.OsmChanged || len(keys) != 3 || keys["name"].After == nil || *keys["name"].After != "Cueva Nueva" ||
		*keys["name"].Before != "Cueva" || keys["ele"].Before != nil || *keys["ele"].After != "1300" || keys["position"].Key == "" {
		t.Errorf("unexpected changes: %+v", got.OsmChanges)
	}
	if got.Name != "Cueva" {
		t.Errorf("a load never changes the place: %q", got.Name)
	}
	flagged := func() map[int64]bool {
		var list []places.ListItem
		rec := e.json("GET", "/api/v1/places", e.member.token, nil)
		status(t, rec, http.StatusOK)
		list = decode[[]places.ListItem](t, rec)
		out := map[int64]bool{}
		for _, it := range list {
			out[it.ID] = it.OsmChanged
		}
		return out
	}
	if f := flagged(); !f[p.ID] || f[plain.ID] {
		t.Errorf("the list flags the place with changes only: %+v", f)
	}

	// Looking at it clears the changes, without touching who changed the place last.
	before := get().UpdatedAt
	rec := e.json("POST", placePath(p, "/links/osm/review"), e.member.token, nil)
	status(t, rec, http.StatusOK)
	reviewed := decode[places.Place](t, rec)
	if reviewed.OsmChanged || len(reviewed.OsmChanges) != 0 || !reviewed.UpdatedAt.Equal(before) {
		t.Errorf("reviewed: %+v, updated %v -> %v", reviewed.OsmChanges, before, reviewed.UpdatedAt)
	}
	if f := flagged(); f[p.ID] {
		t.Error("the list no longer flags it")
	}

	// The element going is a change too, and linking again starts from the element as it is.
	if _, err := repo.Apply(ctx, "spain", nil); err != nil {
		t.Fatal(err)
	}
	if c := get().OsmChanges; len(c) != 1 || c[0].Key != "removed" {
		t.Errorf("the element is gone: %+v", c)
	}

	errorCode(t, e.json("POST", placePath(plain, "/links/osm/review"), e.member.token, nil), 409, "place_not_linked")
	errorCode(t, e.json("POST", "/api/v1/places/999999/links/osm/review", e.member.token, nil), 404, "not_found")
	if rec := e.json("POST", placePath(p, "/links/osm/review"), "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

package places_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

// The subtypes of a cavity: seeded, listed with how many places have each; an admin adds, renames and
// deletes them; names are unique ignoring case; one in use is not deleted.
func TestSubtypes(t *testing.T) {
	e := newEnv(t)
	list := decode[[]places.Subtype](t, e.json("GET", "/api/v1/places/subtypes", e.member.token, nil))
	var names []string
	for _, s := range list {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Cueva,Sima,Torca,Mina,Abrigo" || list[0].ID != e.subCueva {
		t.Fatalf("expected the seeded subtypes: %v", names)
	}

	rec := e.json("POST", "/api/v1/places/subtypes", e.admin.token, map[string]any{"name": "  Sumidero  "})
	status(t, rec, http.StatusCreated)
	sumidero := decode[places.Subtype](t, rec)
	if sumidero.Name != "Sumidero" || sumidero.PlaceCount != 0 {
		t.Errorf("unexpected subtype: %+v", sumidero)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el subtipo de cavidad «Sumidero»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("POST", "/api/v1/places/subtypes", e.admin.token, map[string]any{"name": "sumidero"}), 409, "duplicate_subtype")
	errorCode(t, e.json("POST", "/api/v1/places/subtypes", e.admin.token, map[string]any{"name": "cueva"}), 409, "duplicate_subtype")
	errorCode(t, e.json("POST", "/api/v1/places/subtypes", e.admin.token, map[string]any{"name": "  "}), 400, "subtype_name_required")
	errorCode(t, e.json("POST", "/api/v1/places/subtypes", e.member.token, map[string]any{"name": "Otra"}), 403, "forbidden")

	path := fmt.Sprintf("/api/v1/places/subtypes/%d", sumidero.ID)
	status(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "SUMIDERO"}), http.StatusOK)
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "sima"}), 409, "duplicate_subtype")
	errorCode(t, e.json("PUT", "/api/v1/places/subtypes/987654", e.admin.token, map[string]any{"name": "X"}), 404, "not_found")
	e.notifier.wait(t)

	// One in use is not deleted, a free one is.
	e.newPlace(e.cavidad, "Un sumidero", map[string]any{"subtype_id": sumidero.ID})
	e.notifier.wait(t)
	errorCode(t, e.json("DELETE", path, e.admin.token, nil), 409, "subtype_in_use")
	unused := decode[places.Subtype](t, e.json("POST", "/api/v1/places/subtypes", e.admin.token, map[string]any{"name": "Pozo"}))
	e.notifier.wait(t)
	unusedPath := fmt.Sprintf("/api/v1/places/subtypes/%d", unused.ID)
	status(t, e.json("DELETE", unusedPath, e.admin.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", unusedPath, e.admin.token, nil), 404, "not_found")
	errorCode(t, e.json("DELETE", path, e.member.token, nil), 403, "forbidden")
}

// A cavity has a subtype, and only a cavity: it is required for one, refused for any other type, and
// has to exist; it comes back with its name, and an edit may change it.
func TestSubtypeOfACavity(t *testing.T) {
	e := newEnv(t)
	create := func(typeID int64, extra map[string]any) *httptest.ResponseRecorder {
		b := body(typeID, "Sitio", extra)
		if _, given := extra["subtype_id"]; !given {
			delete(b, "subtype_id") // body gives a cavity its Cueva; these tests say what they send.
		}
		return e.json("POST", "/api/v1/places", e.member.token, b)
	}
	errorCode(t, create(e.cavidad, nil), 400, "place_subtype_required")
	errorCode(t, create(e.cavidad, map[string]any{"subtype_id": nil}), 400, "place_subtype_required")
	errorCode(t, create(e.cavidad, map[string]any{"subtype_id": 987654}), 400, "unknown_subtype")
	errorCode(t, create(e.canyon, map[string]any{"subtype_id": e.subCueva}), 400, "invalid_place_fields")
	errorCode(t, create(e.ferrata, map[string]any{"subtype_id": e.subCueva}), 400, "invalid_place_fields")
	rec := create(e.cavidad, map[string]any{"subtype_id": e.subSima})
	status(t, rec, http.StatusCreated)
	p := decode[places.Place](t, rec)
	if p.SubtypeID == nil || *p.SubtypeID != e.subSima || p.Subtype == nil || *p.Subtype != "Sima" || p.TypeName != "Cavidad" {
		t.Errorf("expected the subtype to come back with its name: %+v", p)
	}
	e.notifier.wait(t)

	// It is in the listing too, and an edit changes it (the notice says so) or fails without one.
	var listed places.ListItem
	for _, it := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		if it.ID == p.ID {
			listed = it
		}
	}
	if listed.Subtype == nil || *listed.Subtype != "Sima" {
		t.Errorf("the listing should have the subtype: %+v", listed)
	}
	rec = e.json("PUT", placePath(p, ""), e.other.token, body(e.cavidad, "Sitio", map[string]any{"subtype_id": e.subCueva}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); *got.Subtype != "Cueva" {
		t.Errorf("the subtype should change: %+v", got.Subtype)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "Subtype: Sima → Cueva") {
		t.Errorf("the notice should say the subtype changed: %q", msg)
	}
	bad := body(e.cavidad, "Sitio", nil)
	delete(bad, "subtype_id")
	errorCode(t, e.json("PUT", placePath(p, ""), e.other.token, bad), 400, "place_subtype_required")
	// Changing the type to a canyon takes the subtype away with the rest of what a cavity has.
	rec = e.json("PUT", placePath(p, ""), e.other.token, body(e.canyon, "Sitio", nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.Subtype != nil || got.SubtypeID != nil || got.Kind != places.KindCanyon {
		t.Errorf("a canyon has no subtype: %+v", got)
	}
}

// Only the types that have a state give their places one: the state is refused for a hut, which has
// none, and is open by default for the rest; a new type says whether it has one, and that stays.
func TestStateOnlyForTypesWithState(t *testing.T) {
	e := newEnv(t)
	hut := e.newPlace(e.refugio, "Refugio de la Sierra", nil)
	e.notifier.wait(t)
	if hut.State != "" {
		t.Errorf("a hut has no state: %+v", hut)
	}
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.refugio, "Otro refugio", map[string]any{"state": "closed"})), 400, "invalid_place_fields")
	// It is absent from the JSON, not an empty text.
	if rec := e.json("GET", placePath(hut, ""), e.member.token, nil); strings.Contains(rec.Body.String(), `"state"`) {
		t.Errorf("a place without a state should not carry the field: %s", rec.Body.String())
	}
	if f := e.newPlace(e.ferrata, "La Ferrata", nil); f.State != places.StateOpen {
		t.Errorf("a via ferrata has a state, open by default: %+v", f)
	}
	e.notifier.wait(t)

	// Changing the type to one without a state takes it away; to one with it, gives the default.
	rec := e.json("PUT", placePath(hut, ""), e.member.token, body(e.ferrata, "Refugio de la Sierra", map[string]any{"state": "tourist"}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.State != places.StateTourist {
		t.Errorf("expected the state it was given: %+v", got)
	}
	rec = e.json("PUT", placePath(hut, ""), e.member.token, body(e.refugio, "Refugio de la Sierra", nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.State != "" {
		t.Errorf("a hut again has none: %+v", got)
	}

	// A new type: with a state or without, which is fixed.
	rec = e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "Mirador", "has_state": true})
	status(t, rec, http.StatusCreated)
	mirador := decode[places.Type](t, rec)
	rec = e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "Fuente"})
	status(t, rec, http.StatusCreated)
	fuente := decode[places.Type](t, rec)
	if !mirador.HasState || fuente.HasState || mirador.Kind != places.KindGeneral {
		t.Errorf("unexpected types: %+v %+v", mirador, fuente)
	}
	if got := e.newPlace(mirador.ID, "Mirador del Pico", nil); got.State != places.StateOpen {
		t.Errorf("a type that has a state gives its places one: %+v", got)
	}
	if got := e.newPlace(fuente.ID, "Fuente del Pino", nil); got.State != "" {
		t.Errorf("a type that has none gives its places none: %+v", got)
	}
}

// The other names are a list: trimmed, without blanks or repeats (ignoring case), at most 20 of at most
// 200 characters, in the order given, and always there.
func TestAltNames(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", map[string]any{
		"alt_names": []string{"  Cueva de Abajo ", "", "cueva de abajo", "Sima Honda", "   "},
	})
	if strings.Join(p.AltNames, "|") != "Cueva de Abajo|Sima Honda" {
		t.Errorf("expected them clean, in order: %q", p.AltNames)
	}
	e.notifier.wait(t)
	none := e.newPlace(e.cavidad, "Sin nombres", nil)
	if none.AltNames == nil || len(none.AltNames) != 0 {
		t.Errorf("expected an empty list: %#v", none.AltNames)
	}
	if rec := e.json("GET", placePath(none, ""), e.member.token, nil); !strings.Contains(rec.Body.String(), `"alt_names":[]`) {
		t.Errorf("the list is there even when empty: %s", rec.Body.String())
	}
	e.notifier.wait(t)

	many := make([]string, places.MaxAltNames+1)
	for i := range many {
		many[i] = fmt.Sprintf("Nombre %d", i)
	}
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Con muchos", map[string]any{"alt_names": many})), 400, "invalid_alt_names")
	status(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Con veinte", map[string]any{"alt_names": many[:places.MaxAltNames]})), http.StatusCreated)
	e.notifier.wait(t)
	long := strings.Repeat("a", places.MaxAltNameLength+1)
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Con uno largo", map[string]any{"alt_names": []string{long}})), 400, "invalid_alt_names")
	status(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Con uno justo", map[string]any{"alt_names": []string{long[1:]}})), http.StatusCreated)
	e.notifier.wait(t)

	// An edit changes the list, and the notice says how.
	rec := e.json("PUT", placePath(p, ""), e.other.token, body(e.cavidad, "Cueva del Agua", map[string]any{"alt_names": []string{"Cueva de Abajo"}}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); strings.Join(got.AltNames, "|") != "Cueva de Abajo" {
		t.Errorf("unexpected names: %q", got.AltNames)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "AltNames: [Cueva de Abajo Sima Honda] → [Cueva de Abajo]") {
		t.Errorf("the notice should say the names changed: %q", msg)
	}
}

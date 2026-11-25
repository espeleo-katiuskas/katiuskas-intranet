package places_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

// A partial edit changes only the fields it names: the rest of the place is as it was, a field given
// as null is cleared, and it says who changed it, like a whole edit.
func TestPatch(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Sima de la Peña", map[string]any{
		"depth_m": 90, "access": "Por la pista", "alt_names": []string{"La Peña"}, "altitude_m": 1200,
	})
	e.notifier.wait(t)

	rec := e.json("PATCH", placePath(p, ""), e.other.token, map[string]any{"lat": 40.1, "lon": -3.2, "altitude_m": 1250})
	status(t, rec, http.StatusOK)
	got := decode[places.Place](t, rec)
	if got.Lat != 40.1 || got.Lon != -3.2 || *got.AltitudeM != 1250 {
		t.Errorf("the named fields should change: %+v", got)
	}
	if got.Name != p.Name || *got.DepthM != 90 || *got.Access != "Por la pista" || len(got.AltNames) != 1 ||
		got.TypeID != e.cavidad || got.AreaID != valdemanco || *got.SubtypeID != e.subCueva {
		t.Errorf("what is not named should stay as it was: %+v", got)
	}
	if got.CreatedBy != e.member.user.Login || got.UpdatedBy != e.other.user.Login {
		t.Errorf("it should say who changed it: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha editado el lugar «Sima de la Peña»") || !strings.Contains(msg, "AltitudeM") {
		t.Errorf("unexpected edit notice: %q", msg)
	}

	// null clears a field that may be empty; the rest stays.
	rec = e.json("PATCH", placePath(p, ""), e.member.token, map[string]any{"depth_m": nil, "access": nil})
	status(t, rec, http.StatusOK)
	got = decode[places.Place](t, rec)
	if got.DepthM != nil || got.Access != nil || *got.AltitudeM != 1250 || got.Name != "Sima de la Peña" {
		t.Errorf("null should clear only what it names: %+v", got)
	}
	e.notifier.wait(t)

	// A partial edit that changes nothing is fine and sends no notice.
	status(t, e.json("PATCH", placePath(p, ""), e.member.token, map[string]any{"name": "Sima de la Peña"}), http.StatusOK)
}

func TestPatchErrors(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	e.notifier.wait(t)
	patch := func(b any) *httptest.ResponseRecorder { return e.json("PATCH", placePath(p, ""), e.member.token, b) }

	errorCode(t, patch(map[string]any{}), 400, "invalid_body")
	errorCode(t, patch(map[string]any{"osm_type": "node", "osm_id": 1}), 400, "invalid_body")
	errorCode(t, patch(map[string]any{"colour": "red"}), 400, "invalid_body")
	errorCode(t, patch(map[string]any{"depth_m": "deep"}), 400, "invalid_body")
	errorCode(t, patch(map[string]any{"lat": 91}), 400, "invalid_coordinates")
	errorCode(t, patch(map[string]any{"lat": nil}), 400, "invalid_coordinates")
	errorCode(t, patch(map[string]any{"name": "  "}), 400, "place_name_required")
	errorCode(t, patch(map[string]any{"altitude_m": 99999}), 400, "invalid_altitude")
	// The result has to be a valid place: a cavity's subtype cannot be taken off.
	errorCode(t, patch(map[string]any{"subtype_id": nil}), 400, "place_subtype_required")
	// ...and another type with the fields of a cavity is refused, as in a whole edit.
	errorCode(t, patch(map[string]any{"type_id": e.refugio}), 400, "invalid_place_fields")
	errorCode(t, e.json("PATCH", "/api/v1/places/987654", e.member.token, map[string]any{"name": "X"}), 404, "not_found")
	errorCode(t, e.json("PATCH", placePath(p, ""), "", map[string]any{"name": "X"}), 401, "unauthorized")

	// Nothing was written by any of them.
	got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	if got.Name != "Cueva del Agua" || got.Lat != 40.5 || got.SubtypeID == nil {
		t.Errorf("a refused edit should change nothing: %+v", got)
	}
}

// Somebody saving the place between the read and the write of a partial edit makes it start again on
// the new place, so neither change is lost.
func TestPatchDoesNotOverwriteAConcurrentEdit(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	e.notifier.wait(t)
	service := places.NewService(places.NewRepository(e.tx), e.storage, accounts.NewService(accounts.NewRepository(e.tx)))
	service.SetActivities(e.activities)
	calls := 0
	got, err := service.PatchPlace(context.Background(), p.ID, e.accountOf(e.member), func(in places.Input) (places.Input, error) {
		calls++
		if calls == 1 {
			// Another person saves the name while this edit is being worked out.
			status(t, e.json("PUT", placePath(p, ""), e.other.token, body(e.cavidad, "Cueva del Agua (otra)", nil)), http.StatusOK)
			e.notifier.wait(t)
		}
		alt := 777
		in.AltitudeM = &alt
		return in, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("the edit should have started again once, it was applied %d times", calls)
	}
	if got.Name != "Cueva del Agua (otra)" || *got.AltitudeM != 777 {
		t.Errorf("both changes should be there: %+v", got)
	}
}

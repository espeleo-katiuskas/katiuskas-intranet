package places_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

func visibilityPath(p places.Place) string { return placePath(p, "/settings/visibility") }

// A new place is for the club alone, at every level, and says so.
func TestANewPlaceIsClosed(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	e.notifier.wait(t)
	got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	want := places.Visibility{Place: places.AudienceClub, Location: places.AudienceClub, Content: places.AudienceClub}
	if got.Visibility != want {
		t.Errorf("visibility %+v, want %+v", got.Visibility, want)
	}
	if len(got.Withheld) != 0 {
		t.Errorf("a member of the club has nothing withheld, got %v", got.Withheld)
	}
	var listed places.ListItem
	for _, it := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		if it.ID == p.ID {
			listed = it
		}
	}
	if listed.Visibility != want {
		t.Errorf("the listing says %+v, want %+v", listed.Visibility, want)
	}
}

// Only admin and board change how open a place is (a member who made it does not), the whole of it at
// once, and it is a change of the place like any other: it says who and when, and it is announced.
func TestSetVisibility(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	e.notifier.wait(t)
	open := map[string]any{"place": "public", "location": "club", "content": "club", "protected": false}

	// The member who made it, and another one.
	for _, token := range []string{e.member.token, e.other.token} {
		errorCode(t, e.json("PUT", visibilityPath(p), token, open), http.StatusForbidden, "forbidden")
	}
	rec := e.json("PUT", visibilityPath(p), e.board.token, open)
	status(t, rec, http.StatusOK)
	got := decode[places.Place](t, rec)
	if got.Visibility.Place != places.AudiencePublic || got.Visibility.Location != places.AudienceClub {
		t.Errorf("the answer must be the place as it is now, got %+v", got.Visibility)
	}
	if got.UpdatedBy != e.board.user.Login {
		t.Errorf("updated_by %q, want the board member %q", got.UpdatedBy, e.board.user.Login)
	}
	msg := e.notifier.wait(t)
	if !strings.Contains(msg, "ha cambiado la visibilidad del lugar «Cueva del Agua»") || !strings.Contains(msg, "el lugar pasa de solo el club a público") {
		t.Errorf("notice %q", msg)
	}
	if strings.Contains(msg, "la localización") {
		t.Errorf("only what changed is said: %q", msg)
	}

	// The same again says nothing; admin may; protecting says so.
	status(t, e.json("PUT", visibilityPath(p), e.admin.token, open), http.StatusOK)
	closed := map[string]any{"place": "club", "location": "club", "content": "club", "protected": true}
	status(t, e.json("PUT", visibilityPath(p), e.admin.token, closed), http.StatusOK)
	msg = e.notifier.wait(t)
	if !strings.Contains(msg, "el lugar pasa de público a solo el club") || !strings.Contains(msg, "queda protegido") {
		t.Errorf("notice %q", msg)
	}
	read := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	if !read.Visibility.Protected || read.Visibility.Place != places.AudienceClub {
		t.Errorf("a read says %+v", read.Visibility)
	}

	// Refused: the levels that contradict each other, an unknown value, a protected place with
	// something public, a place that is not there, and a body that is not JSON.
	errorCode(t, e.json("PUT", visibilityPath(p), e.board.token,
		map[string]any{"place": "club", "location": "public", "content": "club"}), http.StatusBadRequest, "visibility_too_open")
	errorCode(t, e.json("PUT", visibilityPath(p), e.board.token,
		map[string]any{"place": "everyone", "location": "club", "content": "club"}), http.StatusBadRequest, "invalid_place_visibility")
	errorCode(t, e.json("PUT", visibilityPath(p), e.board.token, map[string]any{}), http.StatusBadRequest, "invalid_place_visibility")
	errorCode(t, e.json("PUT", visibilityPath(p), e.board.token,
		map[string]any{"place": "public", "location": "club", "content": "club", "protected": true}), http.StatusBadRequest, "place_protected")
	errorCode(t, e.json("PUT", "/api/v1/places/987654/settings/visibility", e.board.token, open), http.StatusNotFound, "not_found")
	errorCode(t, e.json("PUT", visibilityPath(p), e.board.token, "not an object"), http.StatusBadRequest, "invalid_body")
}

// The database says the same as the service, so that nothing else can write what must not be.
func TestTheDatabaseKeepsTheVisibilityRules(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	for name, set := range map[string]string{
		"an unknown audience":             `place_audience = 'everyone'`,
		"the location more open":          `location_audience = 'public'`,
		"the content more open":           `content_audience = 'public'`,
		"protected with the place public": `protected = true, place_audience = 'public'`,
		"protected with the content open": `protected = true, place_audience = 'public', content_audience = 'public'`,
	} {
		if _, err := e.tx.Exec(t.Context(), `SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
		if _, err := e.tx.Exec(t.Context(), `UPDATE catalogue.places SET `+set+` WHERE id = $1`, p.ID); err == nil {
			t.Errorf("%s: the database accepted it", name)
		}
		if _, err := e.tx.Exec(t.Context(), `ROLLBACK TO SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.tx.Exec(t.Context(), `UPDATE catalogue.places SET place_audience = 'public', location_audience = 'public' WHERE id = $1`, p.ID); err != nil {
		t.Errorf("a place and its location public must be accepted: %v", err)
	}
}

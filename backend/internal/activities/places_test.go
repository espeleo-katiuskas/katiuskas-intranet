package activities_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// insertPlace puts a cave in the catalogue straight in the database (the places have their own tests).
func insertPlace(t *testing.T, tx pgx.Tx, createdBy int64, name string) int64 {
	t.Helper()
	var id int64
	err := tx.QueryRow(context.Background(), `
		INSERT INTO catalogue.places (type_id, kind, has_state, state, subtype_id, name, area_id, lat, lon, created_by, updated_by)
		SELECT t.id, t.kind, t.has_state, 'open', (SELECT id FROM catalogue.place_subtypes WHERE name = 'Cueva'), $2,
			(SELECT id FROM catalogue.areas WHERE parent_id IS NULL AND code = 'ES'), 40.8, -3.6, $1, $1
		FROM catalogue.place_types t WHERE t.name = 'Cavidad'
		RETURNING id
	`, createdBy, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert place: %v", err)
	}
	return id
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return v
}

// An activity goes to places of the catalogue: given when it is created, replaced afterwards by who may
// edit it, listed by anybody, and seen from each place.
func TestHandlerActivityPlaces(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
	board := testutil.InsertMember(t, tx)
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	otherToken := tokenFor(t, authn, other.ID, "member")
	boardToken := tokenFor(t, authn, board.ID, "board")
	sima := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Sima Alfa")
	cueva := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Cueva Beta")
	lejana := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Cueva Gamma")

	// Created with places (a repeated id counts once).
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Fin de semana", "type": "caving", "start_date": "2026-05-02", "end_date": "2026-05-03",
		"place_ids": []int64{cueva, sima, cueva},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	activity := decodeJSON[activities.Activity](t, rec.Body.Bytes())
	notifier.waitForMessage(t)
	path := fmt.Sprintf("/api/v1/activities/%d/places", activity.ID)
	list := decodeJSON[[]activities.PlaceRef](t, doRequest(mux, http.MethodGet, path, otherToken, nil).Body.Bytes())
	if len(list) != 2 || list[0].Name != "Cueva Beta" || list[1].Name != "Sima Alfa" || list[0].TypeName != "Cueva" {
		t.Errorf("expected the two places by name, got %+v", list)
	}

	// Only who may edit the activity changes its places.
	body := map[string]any{"place_ids": []int64{lejana}}
	if rec := doRequest(mux, http.MethodPut, path, otherToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("a member that is not the organizer must be refused, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPut, path, "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
	rec = doRequest(mux, http.MethodPut, path, leaderToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("the organizer changes the places: %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeJSON[[]activities.PlaceRef](t, rec.Body.Bytes()); len(got) != 1 || got[0].ID != lejana {
		t.Errorf("expected the answer to be the new places, got %+v", got)
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, "ha cambiado los lugares de la actividad «Fin de semana»: «Cueva Gamma»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// The same places again say nothing; board may change any activity's; none clears them.
	if rec := doRequest(mux, http.MethodPut, path, leaderToken, body); rec.Code != http.StatusOK {
		t.Fatalf("same places: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPut, path, boardToken, map[string]any{"place_ids": []int64{}}); rec.Code != http.StatusOK {
		t.Fatalf("board clears the places: %d: %s", rec.Code, rec.Body.String())
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, "los lugares de la actividad «Fin de semana»: ninguno") {
		t.Errorf("expected the next notice to be the one that clears them (none for the unchanged), got %q", msg)
	}
	if got := decodeJSON[[]activities.PlaceRef](t, doRequest(mux, http.MethodGet, path, otherToken, nil).Body.Bytes()); len(got) != 0 {
		t.Errorf("expected no places, got %+v", got)
	}

	// Refused: a place that is not there, too many, and an activity that is not there.
	if rec := doRequest(mux, http.MethodPut, path, leaderToken, map[string]any{"place_ids": []int64{987654}}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "unknown_place" {
		t.Errorf("expected unknown_place, got %d: %s", rec.Code, rec.Body.String())
	}
	many := make([]int64, activities.MaxPlaces+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if rec := doRequest(mux, http.MethodPut, path, leaderToken, map[string]any{"place_ids": many}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "too_many_places" {
		t.Errorf("expected too_many_places, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Con lugar falso", "type": "caving", "start_date": "2026-05-09", "end_date": "2026-05-09", "place_ids": []int64{987654},
	}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "unknown_place" {
		t.Errorf("expected an unknown place to refuse the creation, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/activities/987654/places", leaderToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}

	// Seen from a place: its activities, the latest first, for anybody who signs in.
	doRequest(mux, http.MethodPut, path, leaderToken, map[string]any{"place_ids": []int64{sima}})
	notifier.waitForMessage(t)
	later := decodeJSON[activities.Activity](t, doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Otra vez", "type": "caving", "start_date": "2026-06-06", "end_date": "2026-06-06", "place_ids": []int64{sima},
	}).Body.Bytes())
	notifier.waitForMessage(t)
	byPlace := decodeJSON[[]activities.Activity](t, doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/places/%d/activities", sima), otherToken, nil).Body.Bytes())
	if len(byPlace) != 2 || byPlace[0].ID != later.ID || byPlace[1].ID != activity.ID || byPlace[0].LeaderLogin == "" {
		t.Errorf("expected the two activities of the place, latest first, got %+v", byPlace)
	}
	if got := decodeJSON[[]activities.Activity](t, doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/places/%d/activities", cueva), otherToken, nil).Body.Bytes()); len(got) != 0 {
		t.Errorf("expected a place nobody went to to have none, got %+v", got)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/places/987654/activities", otherToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a place that is not there, got %d", rec.Code)
	}
}

// Deleting an activity takes its links to places with it.
func TestHandlerDeleteActivityUnlinksItsPlaces(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertMember(t, tx)
	token := tokenFor(t, authn, leader.ID, "member")
	place := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Sima Delta")
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", token, map[string]any{
		"title": "Pasa", "type": "caving", "start_date": "2026-05-02", "end_date": "2026-05-02", "place_ids": []int64{place},
	})
	activity := decodeJSON[activities.Activity](t, rec.Body.Bytes())
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d", activity.ID), token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d: %s", rec.Code, rec.Body.String())
	}
	var links int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM activity_places WHERE place_id = $1`, place).Scan(&links); err != nil || links != 0 {
		t.Errorf("expected no links left, got %d, %v", links, err)
	}
}

// A place cannot be taken off an activity while an entry of its memoria is about it; the ones that
// stay are never touched.
func TestHandlerPlaceOfTheMemoriaStaysOnTheActivity(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertMember(t, tx)
	token := tokenFor(t, authn, leader.ID, "member")
	boca := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Sima Boca")
	otra := insertPlace(t, tx, testutil.CatalogueAccount(t, tx, leader), "Sima Otra")
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", token, map[string]any{
		"title": "Con memoria", "type": "caving", "start_date": "2026-05-02", "end_date": "2026-05-02", "place_ids": []int64{boca, otra},
	})
	activity := decodeJSON[activities.Activity](t, rec.Body.Bytes())
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO activity_resources (activity_id, type, author_id, title, description, place_id, position)
		VALUES ($1, 'text', $2, 'Crónica', 'x', $3, 1)
	`, activity.ID, leader.ID, boca); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/activities/%d/places", activity.ID)

	// Keeping the place of the memoria, and dropping the other one, is fine.
	if rec := doRequest(mux, http.MethodPut, path, token, map[string]any{"place_ids": []int64{boca}}); rec.Code != http.StatusOK {
		t.Fatalf("dropping the other place: %d: %s", rec.Code, rec.Body.String())
	}
	// Dropping the one the memoria is about is refused, and nothing changes.
	rec = doRequest(mux, http.MethodPut, path, token, map[string]any{"place_ids": []int64{otra}})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "place_in_memoria" {
		t.Fatalf("expected 409 place_in_memoria, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeJSON[[]activities.PlaceRef](t, doRequest(mux, http.MethodGet, path, token, nil).Body.Bytes()); len(got) != 1 || got[0].ID != boca {
		t.Errorf("a refused change must leave the places as they were, got %+v", got)
	}
}

package activityresources_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// insertPlace puts a cave in the catalogue straight in the database, and, if activityID is not 0, among
// the places the activity goes to.
func insertPlace(t *testing.T, tx pgx.Tx, createdBy int64, name string, activityID int64) int64 {
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
	if activityID != 0 {
		if _, err := tx.Exec(context.Background(), `INSERT INTO activity_places (activity_id, place_id) VALUES ($1, $2)`, activityID, id); err != nil {
			t.Fatalf("link place: %v", err)
		}
	}
	return id
}

func createWithPlace(t *testing.T, mux *http.ServeMux, activityID int64, token string, fields map[string]string) (int, activityresources.Resource, string) {
	t.Helper()
	body, contentType := multipartBody(t, fields, "", "", nil)
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	var res activityresources.Resource
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return rec.Code, res, ""
	}
	return rec.Code, res, errorCode(t, rec)
}

// An entry of the memoria may be about one of the places of the activity, and only one of them.
func TestResourceAboutAPlaceOfTheActivity(t *testing.T) {
	mux, authn, activityID, people, tx := newTestServerTx(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	boca := insertPlace(t, tx, people.leader.ID, "Sima Boca", activityID)
	other := insertPlace(t, tx, people.leader.ID, "Sima Ajena", 0) // In the catalogue, not on this trip.

	// With none, as always.
	code, res, _ := createWithPlace(t, mux, activityID, token, map[string]string{"type": "text", "title": "Sin lugar", "description": "x"})
	if code != http.StatusCreated || res.PlaceID != nil || res.PlaceName != nil {
		t.Errorf("expected no place, got %d %+v", code, res)
	}
	// With one of its places: the answer names it.
	code, res, _ = createWithPlace(t, mux, activityID, token, map[string]string{
		"type": "text", "title": "En la boca", "description": "x", "place_id": fmt.Sprint(boca),
	})
	if code != http.StatusCreated || res.PlaceID == nil || *res.PlaceID != boca || res.PlaceName == nil || *res.PlaceName != "Sima Boca" {
		t.Fatalf("expected the place, got %d %+v", code, res)
	}
	listed := decodeResources(t, doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, nil, ""))
	if len(listed) != 2 || listed[1].PlaceName == nil || *listed[1].PlaceName != "Sima Boca" {
		t.Errorf("expected the list to say the place, got %+v", listed)
	}
	// A file may be about a place too.
	body, contentType := multipartBody(t, map[string]string{"type": "file", "title": "Foto", "place_id": fmt.Sprint(boca)}, "file", "foto.jpg", []byte("x"))
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a file about a place: %d: %s", rec.Code, rec.Body.String())
	}

	// A place that is in the catalogue but not on the activity, one that does not exist, and a mess.
	for name, id := range map[string]string{"not on the activity": fmt.Sprint(other), "unknown": "987654"} {
		if code, _, ec := createWithPlace(t, mux, activityID, token, map[string]string{"type": "text", "title": "Mal", "description": "x", "place_id": id}); code != http.StatusBadRequest || ec != "place_not_in_activity" {
			t.Errorf("%s: expected 400 place_not_in_activity, got %d %q", name, code, ec)
		}
	}
	if code, _, ec := createWithPlace(t, mux, activityID, token, map[string]string{"type": "text", "title": "Mal", "description": "x", "place_id": "uno"}); code != http.StatusBadRequest || ec != "invalid_id" {
		t.Errorf("not a number: expected 400 invalid_id, got %d %q", code, ec)
	}

	// Changing it afterwards: to another of the places, to none, and to one that is not allowed.
	path := fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID)
	second := insertPlace(t, tx, people.leader.ID, "Sima Segunda", activityID)
	rec = doJSON(mux, http.MethodPatch, path, token, map[string]any{"place_id": second})
	if got := decodeResource(t, rec); rec.Code != http.StatusOK || got.PlaceID == nil || *got.PlaceID != second || *got.PlaceName != "Sima Segunda" || got.Title != "En la boca" {
		t.Errorf("expected the other place and the rest as it was, got %d %+v", rec.Code, got)
	}
	rec = doJSON(mux, http.MethodPatch, path, token, map[string]any{"title": "Otro título"})
	if got := decodeResource(t, rec); got.PlaceID == nil || *got.PlaceID != second {
		t.Errorf("a patch without place_id must leave the place, got %+v", got)
	}
	rec = doJSON(mux, http.MethodPatch, path, token, map[string]any{"place_id": other})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "place_not_in_activity" {
		t.Errorf("expected place_not_in_activity, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(mux, http.MethodPatch, path, token, map[string]any{"place_id": nil})
	if got := decodeResource(t, rec); rec.Code != http.StatusOK || got.PlaceID != nil || got.PlaceName != nil {
		t.Errorf("null must take the place off, got %d %+v", rec.Code, got)
	}
	rec = doJSON(mux, http.MethodPatch, path, token, map[string]any{"place_id": "uno"})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_body" {
		t.Errorf("expected invalid_body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func decodeResource(t *testing.T, rec *httptest.ResponseRecorder) activityresources.Resource {
	t.Helper()
	var res activityresources.Resource
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	return res
}

func decodeResources(t *testing.T, rec *httptest.ResponseRecorder) []activityresources.Resource {
	t.Helper()
	var list []activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return list
}

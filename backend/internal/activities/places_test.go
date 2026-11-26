package activities_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
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

// The places a person has been to: those of the activities they organized or took part in that have started
// and are not cancelled, with how many went and when the last one ended, the latest first. The catalogue says
// what each is called and where it is.
func TestHandlerPlacesVisitedByAPerson(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	ctx := context.Background()
	member := testutil.InsertMember(t, tx)
	nobody := testutil.InsertMember(t, tx)
	memberToken := tokenFor(t, authn, member.ID, "member")
	by := testutil.CatalogueAccount(t, tx, member)
	visited := insertPlace(t, tx, by, "Sima visitada")
	twice := insertPlace(t, tx, by, "Cueva visitada dos veces")
	cancelled := insertPlace(t, tx, by, "Cueva de la cancelada")
	future := insertPlace(t, tx, by, "Cueva futura")
	others := insertPlace(t, tx, by, "Cueva de otros")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// An activity going to a place; with the member as a participant (or not) and in the state wanted.
	activity := func(place int64, participant bool, update string) int64 {
		t.Helper()
		id := testutil.InsertActivity(t, tx)
		exec(`INSERT INTO activity_places (activity_id, place_id) VALUES ($1, $2)`, id, place)
		if participant {
			exec(`INSERT INTO activity_participants (activity_id, user_id, added_by) VALUES ($1, $2, $2)`, id, member.ID)
		}
		if update != "" {
			exec(`UPDATE activities SET `+update+` WHERE id = $1`, id)
		}
		return id
	}
	activity(visited, true, "start_date = current_date - 30, end_date = current_date - 29")
	activity(twice, true, "start_date = current_date - 10, end_date = current_date - 9")
	// An activity that the member organized (not as a participant) counts too, and the latest end date is kept.
	led := activity(twice, false, "start_date = current_date - 3, end_date = current_date - 2")
	exec(`UPDATE activities SET leader_id = $2 WHERE id = $1`, led, member.ID)
	activity(cancelled, true, "cancelled = true")
	activity(future, true, "start_date = current_date + 10, end_date = current_date + 11")
	activity(others, false, "")

	// The dates above are the database's (current_date), which is not the date of the machine running the test
	// between midnight there and midnight in UTC: the end of the latest visit is asked of the database too.
	var latestEnd string
	if err := tx.QueryRow(ctx, `SELECT to_char(current_date - 2, 'YYYY-MM-DD')`).Scan(&latestEnd); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/users/" + member.Login + "/places"
	rec := doRequest(mux, http.MethodGet, path, memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]activities.VisitedPlace](t, rec.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("only the two places that the member went to, not the cancelled, the future or another's: %+v", got)
	}
	if got[0].ID != twice || got[0].Visits != 2 || got[1].ID != visited || got[1].Visits != 1 {
		t.Errorf("the latest first, with how many times: %+v", got)
	}
	if got[0].Name != "Cueva visitada dos veces" || got[0].TypeName != "Cueva" || got[0].Where != "España" ||
		got[0].LastVisit.Format("2006-01-02") != latestEnd {
		t.Errorf("what it is called, what it is, where and when the last one ended: %+v", got[0])
	}
	// Somebody who has been nowhere gets an empty list, and a login that is nobody's is a 404.
	empty := decodeJSON[[]activities.VisitedPlace](t, doRequest(mux, http.MethodGet, "/api/v1/users/"+nobody.Login+"/places", memberToken, nil).Body.Bytes())
	if len(empty) != 0 {
		t.Errorf("no places: %+v", empty)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users/nadie.de.aqui/places", memberToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a login that is nobody's: %d, want 404", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

// bothServices are the real activities and the real catalogue of places, each wired to the other as the
// server wires them.
func bothServices(t *testing.T, tx pgx.Tx) (*activities.Service, *places.Service) {
	t.Helper()
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	catalogue := newPlaces(t, tx, usersService)
	service := activities.NewService(activities.NewRepository(tx), licensesService, rolesService, catalogue)
	catalogue.SetActivities(service)
	return service, catalogue
}

// What the activities tell the catalogue: in how many activities that are not cancelled each place is one of
// the places they go to (a place with none is not in the answer), and whether any activity at all, cancelled
// or not, goes or went there.
func TestActivitiesCountTheirPlaces(t *testing.T) {
	tx := testutil.NewTx(t)
	ctx := context.Background()
	service, _ := bothServices(t, tx)
	by := testutil.CatalogueAccount(t, tx, testutil.InsertMember(t, tx))
	busy := insertPlace(t, tx, by, "Cueva visitada")
	quiet := insertPlace(t, tx, by, "Cueva tranquila")
	onlyCancelled := insertPlace(t, tx, by, "Cueva de la cancelada")
	link := func(place int64, cancelled bool) {
		t.Helper()
		id := testutil.InsertActivity(t, tx)
		if _, err := tx.Exec(ctx, `INSERT INTO activity_places (activity_id, place_id) VALUES ($1, $2)`, id, place); err != nil {
			t.Fatal(err)
		}
		if cancelled {
			if _, err := tx.Exec(ctx, `UPDATE activities SET cancelled = true WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	link(busy, false)
	link(busy, false)
	link(busy, true) // a cancelled activity does not count
	link(onlyCancelled, true)

	counts, err := service.ActivityCounts(ctx, []int64{busy, quiet, onlyCancelled})
	if err != nil {
		t.Fatal(err)
	}
	if counts[busy] != 2 || len(counts) != 1 {
		t.Errorf("two activities for the first and none counted for the others: %v", counts)
	}
	if empty, err := service.ActivityCounts(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no places, no counts: %v %v", empty, err)
	}
	for place, want := range map[int64]bool{busy: true, onlyCancelled: true, quiet: false} {
		if got, err := service.HasActivities(ctx, place); err != nil || got != want {
			t.Errorf("HasActivities(%d) = %v %v, want %v", place, got, err, want)
		}
	}
}

// The two wired together, as the server does: the places say how many activities went to each, and a place
// that activities went to is not deleted (their history stays), until none goes there.
func TestAPlaceThatActivitiesWentToIsNotDeleted(t *testing.T) {
	tx := testutil.NewTx(t)
	ctx := context.Background()
	service, catalogue := bothServices(t, tx)
	by := testutil.CatalogueAccount(t, tx, testutil.InsertMember(t, tx))
	place := insertPlace(t, tx, by, "Cueva con historia")
	activity := testutil.InsertActivity(t, tx)
	if _, err := tx.Exec(ctx, `INSERT INTO activity_places (activity_id, place_id) VALUES ($1, $2)`, activity, place); err != nil {
		t.Fatal(err)
	}
	if got, err := catalogue.GetPlace(ctx, place); err != nil || got.ActivityCount != 1 {
		t.Errorf("the catalogue says how many activities went there: %+v %v", got.ActivityCount, err)
	}
	if err := catalogue.DeletePlace(ctx, place); !errors.Is(err, places.ErrHasActivities) {
		t.Errorf("a place with activities: %v, want ErrHasActivities", err)
	}
	if err := service.Delete(ctx, activity); err != nil {
		t.Fatalf("delete the activity: %v", err)
	}
	if err := catalogue.DeletePlace(ctx, place); err != nil {
		t.Errorf("once no activity goes there it can be deleted: %v", err)
	}
}

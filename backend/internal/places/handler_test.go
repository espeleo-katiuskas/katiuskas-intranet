package places_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

// wait takes the oldest notification, waiting for it a little: they are sent in the background.
func (f *fakeNotifier) wait(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.sent = f.sent[1:]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a notification")
	return ""
}

// cavidadID and cuevaID are the ids of the type Cavidad and of its subtype Cueva, which body gives to a
// place of that type unless the test says another: a cavity must have a subtype.
var cavidadID, cuevaID int64

// valdemanco is the id of the seeded municipality of Valdemanco (Madrid, Comunidad de Madrid, España),
// which the places of the tests are in: the seed is read-only, so it is the same for every test.
var valdemanco int64

type person struct {
	user  testutil.Person
	token string
}

type env struct {
	t        *testing.T
	tx       pgx.Tx
	mux      *http.ServeMux
	notifier *fakeNotifier
	resolver testutil.FakeResolver
	storage  *places.Storage
	service  *places.Service
	// authenticator is the one of the mux, for a second mux built with other rules.
	authenticator *auth.Authenticator
	// activities answers what the catalogue asks of the activities (how many went to a place, and whether any
	// did): the catalogue knows nothing of them, so the tests say it.
	activities *fakeActivities
	// who: two plain members, board and an admin.
	member, other, board, admin person
	// the ids of the seeded types (a via ferrata has a state, a hut has none), of the subtypes cueva and
	// sima, and of the geologies limestone and gypsum.
	cavidad, canyon, ferrata, refugio int64
	subCueva, subSima                 int64
	caliza, yeso                      int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	storage, err := places.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	placesService := places.NewService(places.NewRepository(tx), storage, accounts.NewService(accounts.NewRepository(tx)))
	fake := newFakeActivities()
	placesService.SetActivities(fake)
	places.NewHandler(placesService, authenticator, notifier).RegisterRoutes(mux)
	e := &env{t: t, tx: tx, mux: mux, notifier: notifier, resolver: resolver, storage: storage, activities: fake, service: placesService, authenticator: authenticator}
	e.member = e.person("member")
	e.other = e.person("member")
	e.board = e.person("board")
	e.admin = e.person("admin")
	id := func(name string) int64 {
		var v int64
		if err := tx.QueryRow(context.Background(), `SELECT id FROM catalogue.place_types WHERE name = $1`, name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	e.cavidad, e.canyon, e.ferrata, e.refugio = id("Cavidad"), id("Cañón"), id("Vía ferrata"), id("Refugio")
	subtype := func(name string) int64 {
		var v int64
		if err := tx.QueryRow(context.Background(), `SELECT id FROM catalogue.place_subtypes WHERE name = $1`, name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	e.subCueva, e.subSima = subtype("Cueva"), subtype("Sima")
	cavidadID, cuevaID = e.cavidad, e.subCueva
	if err := tx.QueryRow(context.Background(), `SELECT id FROM catalogue.areas WHERE kind = 'Municipio' AND name = 'Valdemanco'`).Scan(&valdemanco); err != nil {
		t.Fatal(err)
	}
	geology := func(name string) int64 {
		var v int64
		if err := tx.QueryRow(context.Background(), `SELECT id FROM catalogue.place_geologies WHERE name = $1`, name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	e.caliza, e.yeso = geology("Caliza"), geology("Yeso")
	return e
}

func (e *env) person(roles ...string) person {
	u := testutil.NewPerson(e.t)
	return person{user: u, token: testutil.TokenFor(e.resolver, u.ID, roles...)}
}

func (e *env) json(method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func status(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("expected %d, got %d: %s", want, rec.Code, rec.Body.String())
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if rec.Code != wantStatus || body.Error.Code != wantCode {
		t.Fatalf("expected %d %q, got %d %q", wantStatus, wantCode, rec.Code, body.Error.Code)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

// body is a valid body to create a place of the type, which a test changes.
func body(typeID int64, name string, extra map[string]any) map[string]any {
	b := map[string]any{"type_id": typeID, "name": name, "area_id": valdemanco, "lat": 40.5, "lon": -3.7}
	if typeID == cavidadID {
		b["subtype_id"] = cuevaID
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

// newPlace creates a place as the member and returns it.
func (e *env) newPlace(typeID int64, name string, extra map[string]any) places.Place {
	e.t.Helper()
	rec := e.json("POST", "/api/v1/places", e.member.token, body(typeID, name, extra))
	status(e.t, rec, http.StatusCreated)
	return decode[places.Place](e.t, rec)
}

func placePath(p places.Place, rest string) string {
	return fmt.Sprintf("/api/v1/places/%d%s", p.ID, rest)
}

// What each role may do: everybody who can sign in reads, creates and changes places and their
// points; deleting a place is for admin and board; the types are for admin alone; and a role that is
// not on top of a baseline one is refused everywhere.
func TestPermissions(t *testing.T) {
	e := newEnv(t)
	place := e.newPlace(e.cavidad, "Cueva del Agua", nil)
	lonely := testutil.TokenFor(e.resolver, e.member.user.ID, "material") // without member, board or admin

	type call struct {
		method, path string
		body         any
	}
	readers := []call{
		{"GET", "/api/v1/places", nil}, {"GET", placePath(place, ""), nil}, {"GET", "/api/v1/places/types", nil},
		{"GET", "/api/v1/places/geologies", nil},
	}
	writers := []call{
		{"POST", "/api/v1/places", body(e.cavidad, "Otra", nil)},
		{"PUT", placePath(place, ""), body(e.cavidad, "Cambiada", nil)},
		{"PATCH", placePath(place, ""), map[string]any{"name": "Cambiada"}},
		{"POST", placePath(place, "/points"), map[string]any{"kind": "parking", "lat": 40.0, "lon": -3.0}},
	}
	deleters := []call{{"DELETE", "/api/v1/places/987654", nil}}
	typesAdmin := []call{
		{"POST", "/api/v1/places/types", map[string]any{"name": "Fuente"}},
		{"PUT", "/api/v1/places/types/987654", map[string]any{"name": "X"}},
		{"DELETE", "/api/v1/places/types/987654", nil},
		{"POST", "/api/v1/places/geologies", map[string]any{"name": "Basalto"}},
		{"PUT", "/api/v1/places/geologies/987654", map[string]any{"name": "X"}},
		{"DELETE", "/api/v1/places/geologies/987654", nil},
	}
	refused := func(who, token string, calls []call) {
		t.Helper()
		for _, c := range calls {
			if rec := e.json(c.method, c.path, token, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s: %s %s: expected 403, got %d", who, c.method, c.path, rec.Code)
			}
		}
	}
	allowed := func(who, token string, calls []call) {
		t.Helper()
		for _, c := range calls {
			if rec := e.json(c.method, c.path, token, c.body); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Errorf("%s: %s %s: refused with %d", who, c.method, c.path, rec.Code)
			}
		}
	}
	allowed("member", e.member.token, readers)
	allowed("member", e.member.token, writers)
	refused("member", e.member.token, deleters)
	refused("member", e.member.token, typesAdmin)
	refused("board", e.board.token, typesAdmin)
	allowed("board", e.board.token, deleters)
	allowed("admin", e.admin.token, deleters)
	allowed("admin", e.admin.token, typesAdmin)
	refused("a role without member, board or admin", lonely, append(append(append(readers, writers...), deleters...), typesAdmin...))
	if rec := e.json("GET", "/api/v1/places", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: expected 401, got %d", rec.Code)
	}
}

// A place is created and replaced with every field, says who and when, is listed by name without its
// long texts, and is deleted by admin or board, with its points.
func TestPlaceCRUD(t *testing.T) {
	e := newEnv(t)
	rec := e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "  Sima de la Peña  ", map[string]any{
		"alt_names": []string{"La Peña", "  la peña ", "", "Sima de Peña"}, "access": "Por la pista.",
		"description": "Una sima.", "depth_m": 120, "horizontal_length_m": 300, "technical_sheet": "Faltan 3 anclajes.",
		"state": "permit_required",
	}))
	status(t, rec, http.StatusCreated)
	p := decode[places.Place](t, rec)
	if p.Name != "Sima de la Peña" || p.Kind != places.KindCave || p.TypeName != "Cavidad" || *p.Subtype != "Cueva" ||
		p.State != places.StatePermitRequired || strings.Join(p.AltNames, "|") != "La Peña|Sima de Peña" ||
		*p.DepthM != 120 || *p.HorizontalLengthM != 300 || p.Lat != 40.5 || p.Lon != -3.7 ||
		p.CreatedBy != e.member.user.Login || p.UpdatedBy != e.member.user.Login || len(p.Points) != 0 {
		t.Errorf("unexpected place: %+v", p)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el lugar «Sima de la Peña» (Cavidad) en Valdemanco") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// The default state is open, blank names are none and the list of them is always there.
	q := e.newPlace(e.canyon, "Barranco", map[string]any{"alt_names": []string{"   "}})
	if q.State != places.StateOpen || q.AltNames == nil || len(q.AltNames) != 0 || q.Kind != places.KindCanyon || q.Subtype != nil {
		t.Errorf("unexpected defaults: %+v", q)
	}
	e.newPlace(e.ferrata, "abajo", nil)
	e.notifier.wait(t)
	e.notifier.wait(t)

	// The listing is by name, ignoring case, and has no long texts.
	rec = e.json("GET", "/api/v1/places", e.other.token, nil)
	status(t, rec, http.StatusOK)
	var names []string
	for _, it := range decode[[]places.ListItem](t, rec) {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "abajo,Barranco,Sima de la Peña" {
		t.Errorf("expected them by name, got %v", names)
	}
	for _, long := range []string{`"access"`, `"description"`, `"technical_sheet"`, `"points"`} {
		if strings.Contains(rec.Body.String(), long) {
			t.Errorf("the listing should not carry %s", long)
		}
	}

	// Somebody else edits it: an edit replaces everything and says who.
	rec = e.json("PUT", placePath(p, ""), e.other.token, body(e.cavidad, "Sima de la Peña II", map[string]any{"depth_m": 130, "subtype_id": e.subSima}))
	status(t, rec, http.StatusOK)
	edited := decode[places.Place](t, rec)
	if edited.Name != "Sima de la Peña II" || *edited.Subtype != "Sima" || *edited.DepthM != 130 || edited.HorizontalLengthM != nil ||
		edited.Access != nil || len(edited.AltNames) != 0 || edited.State != places.StateOpen {
		t.Errorf("an edit should replace every field: %+v", edited)
	}
	if edited.CreatedBy != e.member.user.Login || edited.UpdatedBy != e.other.user.Login || edited.UpdatedAt.Before(edited.CreatedAt) {
		t.Errorf("it should say who created it and who changed it: %+v", edited)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha editado el lugar «Sima de la Peña» en Valdemanco") || !strings.Contains(msg, "Name:") {
		t.Errorf("unexpected edit notice: %q", msg)
	}
	errorCode(t, e.json("PUT", "/api/v1/places/987654", e.member.token, body(e.cavidad, "X", nil)), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/places/abc", e.member.token, nil), 400, "invalid_id")
	errorCode(t, e.json("GET", "/api/v1/places/987654", e.member.token, nil), 404, "not_found")

	// Deleting takes its points with it.
	status(t, e.json("POST", placePath(p, "/points"), e.member.token, map[string]any{"kind": "parking", "lat": 40.4, "lon": -3.6}), http.StatusCreated)
	e.notifier.wait(t)
	status(t, e.json("DELETE", placePath(p, ""), e.board.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha borrado el lugar «Sima de la Peña II» en Valdemanco") {
		t.Errorf("unexpected delete notice: %q", msg)
	}
	errorCode(t, e.json("GET", placePath(p, ""), e.member.token, nil), 404, "not_found")
	var left int
	if err := e.tx.QueryRow(context.Background(), `SELECT count(*) FROM catalogue.place_points WHERE place_id = $1`, p.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("the points should be deleted with the place: %d %v", left, err)
	}
	errorCode(t, e.json("DELETE", placePath(p, ""), e.board.token, nil), 404, "not_found")
}

// What a place may have depends on the kind of its type: a depth and a horizontal length only for a
// cave, a technical sheet for a cave or a canyon; and a type change has to fit the new kind.
func TestKindRules(t *testing.T) {
	e := newEnv(t)
	create := func(typeID int64, extra map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", "/api/v1/places", e.member.token, body(typeID, "Sitio", extra))
	}
	sheet := map[string]any{"technical_sheet": "Falta la cuerda del P20."}
	measures := map[string]any{"depth_m": 50, "horizontal_length_m": 10}

	status(t, create(e.cavidad, measures), http.StatusCreated)
	status(t, create(e.cavidad, map[string]any{"depth_m": 50, "subtype_id": e.subSima}), http.StatusCreated)
	status(t, create(e.cavidad, sheet), http.StatusCreated)
	status(t, create(e.canyon, sheet), http.StatusCreated)
	errorCode(t, create(e.canyon, map[string]any{"depth_m": 10}), 400, "invalid_place_fields")
	errorCode(t, create(e.canyon, map[string]any{"horizontal_length_m": 10}), 400, "invalid_place_fields")
	errorCode(t, create(e.ferrata, sheet), 400, "invalid_place_fields")
	errorCode(t, create(e.ferrata, measures), 400, "invalid_place_fields")
	status(t, create(e.ferrata, map[string]any{"technical_sheet": "  "}), http.StatusCreated) // a blank one is none

	// A cave can become a canyon only without a depth; the kind follows.
	cave := e.newPlace(e.cavidad, "Hondura", measures)
	errorCode(t, e.json("PUT", placePath(cave, ""), e.member.token, body(e.canyon, "Hondura", measures)), 400, "invalid_place_fields")
	rec := e.json("PUT", placePath(cave, ""), e.member.token, body(e.canyon, "Hondura", nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.Kind != places.KindCanyon || got.TypeName != "Cañón" || got.DepthM != nil {
		t.Errorf("the kind should follow the type: %+v", got)
	}
}

// What is refused: the name, the type, the state, the coordinates (given, and within WGS84), and the
// measures.
func TestValidation(t *testing.T) {
	e := newEnv(t)
	create := func(extra map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Sitio", extra))
	}
	errorCode(t, create(map[string]any{"name": "   "}), 400, "place_name_required")
	errorCode(t, create(map[string]any{"type_id": 0}), 400, "place_type_required")
	errorCode(t, create(map[string]any{"type_id": 987654}), 400, "unknown_place_type")
	errorCode(t, create(map[string]any{"state": "abierta"}), 400, "invalid_place_state")
	// The four conditions to visit it are accepted, and the old states are gone.
	for _, state := range []string{"open", "permit_required", "tourist", "closed"} {
		rec := create(map[string]any{"state": state})
		status(t, rec, http.StatusCreated)
		if got := decode[places.Place](t, rec); string(got.State) != state {
			t.Errorf("expected the state %q, got %q", state, got.State)
		}
	}
	for _, old := range []string{"active", "equipping", "gone", "permit", "OPEN"} {
		errorCode(t, create(map[string]any{"state": old}), 400, "invalid_place_state")
	}
	for name, extra := range map[string]map[string]any{
		"latitude above 90":    {"lat": 90.5},
		"latitude below -90":   {"lat": -91},
		"longitude above 180":  {"lon": 181},
		"longitude below -180": {"lon": -180.5},
	} {
		if rec := create(extra); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_coordinates") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// A missing coordinate is not zero.
	for _, missing := range []string{"lat", "lon"} {
		b := body(e.cavidad, "Sitio", nil)
		delete(b, missing)
		errorCode(t, e.json("POST", "/api/v1/places", e.member.token, b), 400, "invalid_coordinates")
	}
	errorCode(t, create(map[string]any{"depth_m": -1}), 400, "invalid_place_measure")
	errorCode(t, create(map[string]any{"depth_m": places.MaxDepthM + 1}), 400, "invalid_place_measure")
	errorCode(t, create(map[string]any{"horizontal_length_m": places.MaxHorizontalLengthM + 1}), 400, "invalid_place_measure")
	status(t, create(map[string]any{"lat": 0, "lon": 0, "depth_m": 0}), http.StatusCreated) // zero is a real value
	status(t, create(map[string]any{"lat": -90, "lon": 180}), http.StatusCreated)
}

// The other points of a place: any member adds, changes and removes them, the kind and the coordinates
// are checked, and a change of a point counts as a change of the place.
func TestPoints(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva", nil)
	e.notifier.wait(t)
	other := e.newPlace(e.cavidad, "Otra", nil)
	e.notifier.wait(t)

	add := func(extra map[string]any) *httptest.ResponseRecorder {
		b := map[string]any{"kind": "parking", "lat": 40.4, "lon": -3.6}
		for k, v := range extra {
			b[k] = v
		}
		return e.json("POST", placePath(p, "/points"), e.other.token, b)
	}
	rec := add(map[string]any{"name": "  Pista  ", "note": "Dejar el coche aquí."})
	status(t, rec, http.StatusCreated)
	pt := decode[places.Point](t, rec)
	if pt.Kind != places.PointParking || *pt.Name != "Pista" || pt.Lat != 40.4 || pt.PlaceID != p.ID {
		t.Errorf("unexpected point: %+v", pt)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el punto «Pista» (parking) al lugar «Cueva» en Valdemanco") {
		t.Errorf("unexpected notice: %q", msg)
	}
	status(t, add(map[string]any{"kind": "waypoint", "name": "  "}), http.StatusCreated)
	e.notifier.wait(t)
	errorCode(t, add(map[string]any{"kind": "garaje"}), 400, "invalid_point_kind")
	errorCode(t, add(map[string]any{"kind": ""}), 400, "invalid_point_kind")
	errorCode(t, add(map[string]any{"lat": 100}), 400, "invalid_coordinates")
	errorCode(t, e.json("POST", placePath(p, "/points"), e.other.token, map[string]any{"kind": "access", "lat": 40.0}), 400, "invalid_coordinates")
	errorCode(t, e.json("POST", "/api/v1/places/987654/points", e.other.token, map[string]any{"kind": "access", "lat": 40.0, "lon": -3.0}), 404, "not_found")

	// They come with the place, in the order they were added, and the place says who touched it last.
	got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	if len(got.Points) != 2 || got.Points[0].ID != pt.ID || got.Points[1].Name != nil {
		t.Errorf("unexpected points: %+v", got.Points)
	}
	if got.UpdatedBy != e.other.user.Login {
		t.Errorf("a point added by somebody else should be the last change: %q", got.UpdatedBy)
	}

	// Changing and removing: by the member, and only through its own place.
	pointPath := placePath(p, fmt.Sprintf("/points/%d", pt.ID))
	rec = e.json("PUT", pointPath, e.member.token, map[string]any{"kind": "access", "name": "Boca", "lat": 40.41, "lon": -3.61})
	status(t, rec, http.StatusOK)
	if upd := decode[places.Point](t, rec); upd.Kind != places.PointAccess || *upd.Name != "Boca" || upd.Note != nil || upd.Lat != 40.41 {
		t.Errorf("an edit should replace every field: %+v", upd)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha cambiado el punto «Boca» (access) del lugar «Cueva»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("PUT", placePath(other, fmt.Sprintf("/points/%d", pt.ID)), e.member.token, map[string]any{"kind": "access", "lat": 40.0, "lon": -3.0}), 404, "not_found")
	errorCode(t, e.json("PUT", pointPath, e.member.token, map[string]any{"kind": "x", "lat": 40.0, "lon": -3.0}), 400, "invalid_point_kind")
	errorCode(t, e.json("DELETE", placePath(other, fmt.Sprintf("/points/%d", pt.ID)), e.member.token, nil), 404, "not_found")
	status(t, e.json("DELETE", pointPath, e.member.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha quitado el punto «Boca» (access) del lugar «Cueva»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("DELETE", pointPath, e.member.token, nil), 404, "not_found")
	after := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil))
	if len(after.Points) != 1 || after.UpdatedBy != e.member.user.Login {
		t.Errorf("unexpected place after removing a point: %+v", after)
	}
}

// The types: seeded and fixed for caves and canyons, listed with how many places have each; an admin
// adds, renames and deletes the general ones; names are unique ignoring case.
func TestTypes(t *testing.T) {
	e := newEnv(t)
	types := decode[[]places.Type](t, e.json("GET", "/api/v1/places/types", e.member.token, nil))
	if len(types) < 5 || types[0].Name != "Cavidad" || types[0].Kind != places.KindCave || !types[0].HasState ||
		types[1].Kind != places.KindCanyon || !types[1].HasState || types[2].Kind != places.KindGeneral {
		t.Fatalf("expected the seeded types, the cavities first: %+v", types)
	}
	withState := map[string]bool{}
	for _, ty := range types {
		withState[ty.Name] = ty.HasState
	}
	if !withState["Vía ferrata"] || withState["Refugio"] || withState["Otro"] {
		t.Errorf("a via ferrata has a state, a hut and the rest do not: %v", withState)
	}

	rec := e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "  Fuente  "})
	status(t, rec, http.StatusCreated)
	fuente := decode[places.Type](t, rec)
	if fuente.Name != "Fuente" || fuente.Kind != places.KindGeneral || fuente.PlaceCount != 0 {
		t.Errorf("unexpected type: %+v", fuente)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el tipo de lugar «Fuente»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "fuente"}), 409, "duplicate_place_type")
	errorCode(t, e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "cavidad"}), 409, "duplicate_place_type")
	errorCode(t, e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "  "}), 400, "place_type_name_required")

	path := fmt.Sprintf("/api/v1/places/types/%d", fuente.ID)
	status(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "FUENTE"}), http.StatusOK) // only the case changes
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "refugio"}), 409, "duplicate_place_type")
	errorCode(t, e.json("PUT", "/api/v1/places/types/987654", e.admin.token, map[string]any{"name": "X"}), 404, "not_found")

	// The types of caves and canyons are fixed.
	for _, id := range []int64{e.cavidad, e.canyon} {
		fixed := fmt.Sprintf("/api/v1/places/types/%d", id)
		errorCode(t, e.json("PUT", fixed, e.admin.token, map[string]any{"name": "Otra cosa"}), 409, "fixed_place_type")
		errorCode(t, e.json("DELETE", fixed, e.admin.token, nil), 409, "fixed_place_type")
	}

	// One in use cannot be deleted, a free one can.
	e.newPlace(fuente.ID, "Fuente del Pino", nil)
	for _, ty := range decode[[]places.Type](t, e.json("GET", "/api/v1/places/types", e.member.token, nil)) {
		if ty.ID == fuente.ID && ty.PlaceCount != 1 {
			t.Errorf("expected 1 place, got %d", ty.PlaceCount)
		}
	}
	errorCode(t, e.json("DELETE", path, e.admin.token, nil), 409, "place_type_in_use")
	unused := decode[places.Type](t, e.json("POST", "/api/v1/places/types", e.admin.token, map[string]any{"name": "Mirador"}))
	status(t, e.json("DELETE", fmt.Sprintf("/api/v1/places/types/%d", unused.ID), e.admin.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", fmt.Sprintf("/api/v1/places/types/%d", unused.ID), e.admin.token, nil), 404, "not_found")
}

// Where a place is: the most specific zone, which says its country, province… as the zones it is in; the
// listing and the place give them, and an edit may move it. The text of where it is has no autonomous
// community when there is a province (in Spain the province says it already), the ids and the path do.
func TestAreaOfAPlace(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Sima de la Peña", nil)
	e.notifier.wait(t)
	if p.AreaID != valdemanco || p.Where != "Valdemanco, Madrid, España" || len(p.AreaIDs) != 4 || p.AreaIDs[3] != valdemanco {
		t.Errorf("unexpected zone: %d %q %v", p.AreaID, p.Where, p.AreaIDs)
	}
	if p.AreaNames != "Valdemanco, Madrid, Comunidad de Madrid, España" {
		t.Errorf("the names for the search keep the community: %q", p.AreaNames)
	}
	var kinds, names []string
	for _, ref := range p.AreaPath {
		kinds, names = append(kinds, ref.Kind), append(names, ref.Name)
	}
	if strings.Join(kinds, ",") != "País,Comunidad autónoma,Provincia,Municipio" || strings.Join(names, ",") != "España,Comunidad de Madrid,Madrid,Valdemanco" {
		t.Errorf("the path should go from the country to the zone: %v %v", kinds, names)
	}
	item := decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil))[0]
	if item.Where != p.Where || item.AreaID != valdemanco || len(item.AreaIDs) != 4 || item.AreaIDs[0] != p.AreaIDs[0] {
		t.Errorf("the listing should say where it is too: %+v", item)
	}

	// In a community with no province, or in a country that has neither, the text says all there is.
	for _, c := range []struct{ name, want string }{{"Comunidad de Madrid", "Comunidad de Madrid, España"}, {"Ceuta", "Ceuta, España"}} {
		name, want := c.name, c.want
		var zone int64
		if err := e.tx.QueryRow(context.Background(), `SELECT id FROM catalogue.areas WHERE name = $1 AND kind IN ('Comunidad autónoma', 'Ciudad autónoma')`, name).Scan(&zone); err != nil {
			t.Fatal(err)
		}
		in := decode[places.Place](t, e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Sima de la Peña", map[string]any{"area_id": zone})))
		e.notifier.wait(t)
		if in.Where != want || len(in.AreaIDs) != 2 {
			t.Errorf("%s: %q %v", name, in.Where, in.AreaIDs)
		}
	}

	// A place may be only in a country, and an edit moves it, saying so in the notice.
	var spain int64
	if err := e.tx.QueryRow(context.Background(), `SELECT id FROM catalogue.areas WHERE parent_id IS NULL AND code = 'ES'`).Scan(&spain); err != nil {
		t.Fatal(err)
	}
	rec := e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Sima de la Peña", map[string]any{"area_id": spain}))
	status(t, rec, http.StatusOK)
	moved := decode[places.Place](t, rec)
	if moved.Where != "España" || len(moved.AreaIDs) != 1 || len(moved.AreaPath) != 1 {
		t.Errorf("a place in a country only: %+v", moved)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "Where: Ceuta, España → España") {
		t.Errorf("the notice should say it moved: %q", msg)
	}

	// The zone is required and has to exist.
	missing := body(e.cavidad, "Otra", nil)
	delete(missing, "area_id")
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, missing), 400, "place_area_required")
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Otra", map[string]any{"area_id": 987654321})), 400, "unknown_area")
	errorCode(t, e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Otra", map[string]any{"area_id": 987654321})), 400, "unknown_area")
}

// What a cave is formed in: a geology of the catalogue, only for a cave, optional, shown in the listing
// and the place, and part of what an edit says it changed.
func TestGeologyOfAPlace(t *testing.T) {
	e := newEnv(t)
	withGeology := func(id int64) map[string]any { return map[string]any{"geology_id": id} }

	p := e.newPlace(e.cavidad, "Cueva de caliza", withGeology(e.caliza))
	e.notifier.wait(t)
	if p.GeologyID == nil || *p.GeologyID != e.caliza || p.Geology == nil || *p.Geology != "Caliza" {
		t.Errorf("a cave with a geology: %+v %+v", p.GeologyID, p.Geology)
	}
	none := e.newPlace(e.cavidad, "Sima sin estudiar", map[string]any{"subtype_id": e.subSima})
	e.notifier.wait(t)
	if none.GeologyID != nil || none.Geology != nil {
		t.Errorf("it is optional: %+v", none.Geology)
	}
	var listed *string
	for _, it := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		if it.ID == p.ID {
			listed = it.Geology
		}
	}
	if listed == nil || *listed != "Caliza" {
		t.Errorf("the listing should say it too: %v", listed)
	}

	// Only a cave has one, and it has to exist.
	for name, typeID := range map[string]int64{"a canyon": e.canyon, "a via ferrata": e.ferrata} {
		rec := e.json("POST", "/api/v1/places", e.member.token, body(typeID, "No es una cueva", withGeology(e.caliza)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_place_fields") {
			t.Errorf("a geology on %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Rara", withGeology(987654))), 400, "unknown_geology")
	errorCode(t, e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Rara", withGeology(0))), 400, "unknown_geology")
	// A cave that becomes a canyon cannot keep it.
	errorCode(t, e.json("PUT", placePath(p, ""), e.member.token, body(e.canyon, "Ahora cañón", withGeology(e.caliza))), 400, "invalid_place_fields")

	// An edit replaces it: another one, and leaving it out clears it; the notice says it.
	rec := e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Cueva de caliza", withGeology(e.yeso)))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.Geology == nil || *got.Geology != "Yeso" {
		t.Errorf("the geology should change: %+v", got.Geology)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "Geology: Caliza → Yeso") {
		t.Errorf("the notice should say the geology changed: %q", msg)
	}
	rec = e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Cueva de caliza", nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.GeologyID != nil || got.Geology != nil {
		t.Errorf("leaving it out should clear it: %+v", got.Geology)
	}
}

// The geologies: seeded (the usual ones first), listed with how many places have each; an admin adds,
// renames and deletes them; names are unique ignoring case; one in use is not deleted.
func TestGeologies(t *testing.T) {
	e := newEnv(t)
	list := decode[[]places.Geology](t, e.json("GET", "/api/v1/places/geologies", e.member.token, nil))
	var names []string
	for _, g := range list {
		names = append(names, g.Name)
	}
	if len(list) < 11 || strings.Join(names[:4], ",") != "Caliza,Yeso,Hielo,Lava" || list[0].ID != e.caliza {
		t.Fatalf("expected the seeded geologies, the usual four first: %v", names)
	}

	rec := e.json("POST", "/api/v1/places/geologies", e.admin.token, map[string]any{"name": "  Basalto  "})
	status(t, rec, http.StatusCreated)
	basalto := decode[places.Geology](t, rec)
	if basalto.Name != "Basalto" || basalto.PlaceCount != 0 {
		t.Errorf("unexpected geology: %+v", basalto)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido la geología «Basalto»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("POST", "/api/v1/places/geologies", e.admin.token, map[string]any{"name": "basalto"}), 409, "duplicate_geology")
	errorCode(t, e.json("POST", "/api/v1/places/geologies", e.admin.token, map[string]any{"name": "caliza"}), 409, "duplicate_geology")
	errorCode(t, e.json("POST", "/api/v1/places/geologies", e.admin.token, map[string]any{"name": "  "}), 400, "geology_name_required")

	path := fmt.Sprintf("/api/v1/places/geologies/%d", basalto.ID)
	status(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "BASALTO"}), http.StatusOK) // only the case changes
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "yeso"}), 409, "duplicate_geology")
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "  "}), 400, "geology_name_required")
	errorCode(t, e.json("PUT", "/api/v1/places/geologies/987654", e.admin.token, map[string]any{"name": "X"}), 404, "not_found")
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha cambiado la geología «Basalto» por «BASALTO»") {
		t.Errorf("unexpected notice: %q", msg)
	}

	// None is fixed: a seeded one is renamed like the others. One in use is not deleted.
	status(t, e.json("PUT", fmt.Sprintf("/api/v1/places/geologies/%d", e.yeso), e.admin.token, map[string]any{"name": "Yeso (evaporita)"}), http.StatusOK)
	e.newPlace(e.cavidad, "En basalto", map[string]any{"geology_id": basalto.ID})
	for _, g := range decode[[]places.Geology](t, e.json("GET", "/api/v1/places/geologies", e.member.token, nil)) {
		if g.ID == basalto.ID && g.PlaceCount != 1 {
			t.Errorf("expected 1 place, got %d", g.PlaceCount)
		}
	}
	errorCode(t, e.json("DELETE", path, e.admin.token, nil), 409, "geology_in_use")
	unused := decode[places.Geology](t, e.json("POST", "/api/v1/places/geologies", e.admin.token, map[string]any{"name": "Toba"}))
	unusedPath := fmt.Sprintf("/api/v1/places/geologies/%d", unused.ID)
	status(t, e.json("DELETE", unusedPath, e.admin.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", unusedPath, e.admin.token, nil), 404, "not_found")
}

// The altitude, in metres above sea level, of the main coordinate and of the other points: optional,
// negative below sea level, within limits that only catch a typo, replaced by an edit, and part of what
// the notice says changed.
func TestAltitude(t *testing.T) {
	e := newEnv(t)
	create := func(extra map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", "/api/v1/places", e.member.token, body(e.cavidad, "Cueva alta", extra))
	}
	rec := create(map[string]any{"altitude_m": 1234})
	status(t, rec, http.StatusCreated)
	p := decode[places.Place](t, rec)
	e.notifier.wait(t)
	if p.AltitudeM == nil || *p.AltitudeM != 1234 {
		t.Errorf("the altitude should come back: %v", p.AltitudeM)
	}
	var listed *int
	for _, it := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		if it.ID == p.ID {
			listed = it.AltitudeM
		}
	}
	if listed == nil || *listed != 1234 {
		t.Errorf("the listing should have it too: %v", listed)
	}
	if none := e.newPlace(e.cavidad, "Sima sin altitud", map[string]any{"subtype_id": e.subSima}); none.AltitudeM != nil {
		t.Errorf("it is optional: %v", *none.AltitudeM)
	}
	e.notifier.wait(t)

	// Zero, below sea level and the limits are fine; beyond them is a typo.
	for _, altitude := range []int{0, -430, places.MinAltitudeM, places.MaxAltitudeM} {
		if rec := create(map[string]any{"altitude_m": altitude}); rec.Code != http.StatusCreated || *decode[places.Place](t, rec).AltitudeM != altitude {
			t.Errorf("altitude %d should be accepted: %d %s", altitude, rec.Code, rec.Body.String())
		}
	}
	for _, altitude := range []int{places.MinAltitudeM - 1, places.MaxAltitudeM + 1, 123456} {
		errorCode(t, create(map[string]any{"altitude_m": altitude}), 400, "invalid_altitude")
	}

	// An edit replaces it, and leaving it out clears it; the notice says it changed.
	rec = e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Cueva alta", map[string]any{"altitude_m": 1250}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); *got.AltitudeM != 1250 {
		t.Errorf("the altitude should change: %v", *got.AltitudeM)
	}
	for i := 0; i < 4; i++ { // the creations with each altitude
		e.notifier.wait(t)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "AltitudeM: 1234 → 1250") {
		t.Errorf("the notice should say the altitude changed: %q", msg)
	}
	rec = e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Cueva alta", nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.AltitudeM != nil {
		t.Errorf("leaving it out should clear it: %v", *got.AltitudeM)
	}
	errorCode(t, e.json("PUT", placePath(p, ""), e.member.token, body(e.cavidad, "Cueva alta", map[string]any{"altitude_m": 9001})), 400, "invalid_altitude")

	// The same in the other points.
	point := func(extra map[string]any) map[string]any {
		b := map[string]any{"kind": "parking", "lat": 40.4, "lon": -3.6}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	rec = e.json("POST", placePath(p, "/points"), e.member.token, point(map[string]any{"altitude_m": 980}))
	status(t, rec, http.StatusCreated)
	pt := decode[places.Point](t, rec)
	if pt.AltitudeM == nil || *pt.AltitudeM != 980 {
		t.Errorf("a point has an altitude too: %v", pt.AltitudeM)
	}
	if got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil)); len(got.Points) != 1 || *got.Points[0].AltitudeM != 980 {
		t.Errorf("the place should bring it with its points: %+v", got.Points)
	}
	errorCode(t, e.json("POST", placePath(p, "/points"), e.member.token, point(map[string]any{"altitude_m": -501})), 400, "invalid_altitude")
	pointPath := placePath(p, fmt.Sprintf("/points/%d", pt.ID))
	rec = e.json("PUT", pointPath, e.member.token, point(map[string]any{"altitude_m": 1010}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Point](t, rec); *got.AltitudeM != 1010 {
		t.Errorf("the altitude of a point should change: %v", *got.AltitudeM)
	}
	rec = e.json("PUT", pointPath, e.member.token, point(nil))
	status(t, rec, http.StatusOK)
	if got := decode[places.Point](t, rec); got.AltitudeM != nil {
		t.Errorf("leaving it out clears it too: %v", *got.AltitudeM)
	}
	errorCode(t, e.json("PUT", pointPath, e.member.token, point(map[string]any{"altitude_m": 9001})), 400, "invalid_altitude")
}

// upload attaches a file to a place as the person, with an optional caption.
func (e *env) upload(p places.Place, token, name, title string, content []byte) *httptest.ResponseRecorder {
	return e.uploadTo(placePath(p, "/files"), token, name, title, content)
}

// uploadTo attaches a file at the path of the files of a place or a system.
func (e *env) uploadTo(path, token, name, title string, content []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if title != "" {
		_ = mw.WriteField("title", title)
	}
	if name != "" {
		part, err := mw.CreateFormFile("file", name)
		if err != nil {
			e.t.Fatal(err)
		}
		_, _ = part.Write(content)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// png is the smallest PNG there is, enough for the type to be recognised from the bytes.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func TestFiles(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva", nil)
	e.notifier.wait(t)

	// Anybody who can sign in attaches a file, with a caption.
	rec := e.upload(p, e.other.token, "C:\\fotos\\boca.png", "  La boca  ", png)
	status(t, rec, http.StatusCreated)
	photo := decode[places.File](t, rec)
	if photo.FileName != "boca.png" || photo.ContentType != "image/png" || *photo.Title != "La boca" ||
		photo.FileSize != int64(len(png)) || photo.PlaceID == nil || *photo.PlaceID != p.ID || photo.SystemID != nil {
		t.Errorf("unexpected file: %+v", photo)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha adjuntado «boca.png» al lugar «Cueva» en Valdemanco") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// A page is never shown by the browser: it is a download (checked below), whatever its name says.
	rec = e.upload(p, e.member.token, "trampa.html", "", []byte("<script>alert(1)</script>"))
	status(t, rec, http.StatusCreated)
	page := decode[places.File](t, rec)
	if page.Title != nil {
		t.Errorf("a file attached without a caption has none: %+v", page)
	}
	e.notifier.wait(t)

	errorCode(t, e.upload(p, e.member.token, "", "", nil), 400, "file_required")
	errorCode(t, e.upload(p, e.member.token, "vacio.txt", "", nil), 400, "file_required")
	errorCode(t, e.upload(p, e.member.token, "a.png", strings.Repeat("x", places.MaxFileTitleLength+1), png), 400, "file_title_too_long")
	errorCode(t, e.upload(p, e.member.token, "grande.bin", "", make([]byte, places.MaxFileSize+1)), 413, "file_too_large")
	missing := places.Place{}
	missing.ID = 987654
	errorCode(t, e.upload(missing, e.member.token, "a.png", "", png), 404, "not_found")

	// The list and the count of the page.
	list := decode[[]places.File](t, e.json("GET", placePath(p, "/files"), e.member.token, nil))
	if len(list) != 2 || list[0].ID != photo.ID || list[1].ID != page.ID {
		t.Errorf("unexpected list: %+v", list)
	}
	if got := decode[places.Place](t, e.json("GET", placePath(p, ""), e.member.token, nil)); got.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", got.FileCount)
	}

	// The bytes: an image is shown, anything else downloaded and sandboxed.
	rec = e.json("GET", placePath(p, fmt.Sprintf("/files/%d/file", photo.ID)), e.member.token, nil)
	status(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), png) || rec.Header().Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("unexpected image answer: %v", rec.Header())
	}
	rec = e.json("GET", placePath(p, fmt.Sprintf("/files/%d/file", page.ID)), e.member.token, nil)
	status(t, rec, http.StatusOK)
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("a page must be downloaded and sandboxed: %v", rec.Header())
	}
	errorCode(t, e.json("GET", placePath(p, "/files/987654/file"), e.member.token, nil), 404, "not_found")
	if rec := e.json("GET", placePath(p, fmt.Sprintf("/files/%d/file", photo.ID)), "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a file needs a session, got %d", rec.Code)
	}

	// Who removes: whoever attached it, admin and board; not another member.
	errorCode(t, e.json("DELETE", placePath(p, fmt.Sprintf("/files/%d", photo.ID)), e.member.token, nil), 403, "place_file_not_yours")
	status(t, e.json("DELETE", placePath(p, fmt.Sprintf("/files/%d", photo.ID)), e.other.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha quitado «boca.png» del lugar «Cueva» en Valdemanco") {
		t.Errorf("unexpected notice: %q", msg)
	}
	if _, err := e.storage.Open(p.ID, photo.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the bytes must be gone, got %v", err)
	}
	status(t, e.json("DELETE", placePath(p, fmt.Sprintf("/files/%d", page.ID)), e.board.token, nil), http.StatusNoContent)
	e.notifier.wait(t)
	errorCode(t, e.json("DELETE", placePath(p, fmt.Sprintf("/files/%d", page.ID)), e.board.token, nil), 404, "not_found")

	// Deleting a place takes its files with it, rows and bytes.
	rec = e.upload(p, e.member.token, "foto.png", "", png)
	status(t, rec, http.StatusCreated)
	kept := decode[places.File](t, rec)
	e.notifier.wait(t)
	status(t, e.json("DELETE", placePath(p, ""), e.admin.token, nil), http.StatusNoContent)
	if _, err := e.storage.Open(p.ID, kept.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the bytes of a deleted place must be gone, got %v", err)
	}
}

// fakeActivities is what the activities say to the catalogue: in how many activities each place is, and
// whether any went there. The catalogue only passes it on; counting them is the activities' own business
// (and is tested there).
type fakeActivities struct {
	counts map[int64]int
	used   map[int64]bool
}

func newFakeActivities() *fakeActivities {
	return &fakeActivities{counts: map[int64]int{}, used: map[int64]bool{}}
}

func (f *fakeActivities) ActivityCounts(_ context.Context, ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	for _, id := range ids {
		if n := f.counts[id]; n > 0 {
			out[id] = n
		}
	}
	return out, nil
}

func (f *fakeActivities) HasActivities(_ context.Context, id int64) (bool, error) {
	return f.used[id], nil
}

// The listing, the place and the cavities of a system say in how many activities the place is one of the
// places they go to, as the activities say it.
func TestActivityCountOfAPlace(t *testing.T) {
	e := newEnv(t)
	busy := e.newPlace(e.cavidad, "Cueva visitada", nil)
	e.notifier.wait(t)
	quiet := e.newPlace(e.cavidad, "Cueva tranquila", nil)
	e.notifier.wait(t)
	e.activities.counts[busy.ID] = 2
	counts := map[int64]int{}
	for _, item := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		counts[item.ID] = item.ActivityCount
	}
	if counts[busy.ID] != 2 || counts[quiet.ID] != 0 {
		t.Errorf("two activities for the first, none for the second: %v", counts)
	}
	if got := decode[places.Place](t, e.json("GET", placePath(busy, ""), e.member.token, nil)); got.ActivityCount != 2 {
		t.Errorf("the place says it too: %d", got.ActivityCount)
	}
}

// A place that activities went to is not deleted: their history stays. Whether any did is for the
// activities to say.
func TestPlaceWithActivitiesIsNotDeleted(t *testing.T) {
	e := newEnv(t)
	p := e.newPlace(e.cavidad, "Cueva con historia", nil)
	e.notifier.wait(t)
	e.activities.used[p.ID] = true
	errorCode(t, e.json("DELETE", placePath(p, ""), e.admin.token, nil), 409, "place_has_activities")
	if rec := e.json("GET", placePath(p, ""), e.member.token, nil); rec.Code != http.StatusOK {
		t.Errorf("the place must still be there, got %d", rec.Code)
	}
	// Once no activity goes there, it can be.
	e.activities.used[p.ID] = false
	status(t, e.json("DELETE", placePath(p, ""), e.admin.token, nil), http.StatusNoContent)
}

// accountOf is the account of the catalogue of a person of the test, made if they have none.
func (e *env) accountOf(p person) int64 {
	e.t.Helper()
	return testutil.CatalogueAccount(e.t, e.tx, p.user)
}

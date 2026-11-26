package publicplaces_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/publicplaces"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// noActivities is the activities of a catalogue that none went to.
type noActivities struct{}

func (noActivities) ActivityCounts(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}
func (noActivities) HasActivities(context.Context, int64) (bool, error) { return false, nil }

type env struct {
	t       *testing.T
	tx      pgx.Tx
	service *places.Service
	by      int64
	typeID  int64
	subtype int64
	area    int64
	member  string // a bearer token of a member of the club
	resolve testutil.FakeResolver
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	storage, err := places.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := places.NewService(places.NewRepository(tx), storage, accounts.NewService(accounts.NewRepository(tx)))
	service.SetActivities(noActivities{})
	resolver := testutil.FakeResolver{}
	user := testutil.NewPerson(t)
	e := &env{t: t, tx: tx, service: service, by: testutil.CatalogueAccount(t, tx, user), resolve: resolver}
	e.member = testutil.TokenFor(resolver, user.ID, "member")
	one := func(sql string) int64 {
		var id int64
		if err := tx.QueryRow(context.Background(), sql).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.typeID = one(`SELECT id FROM catalogue.place_types WHERE name = 'Cavidad'`)
	e.subtype = one(`SELECT id FROM catalogue.place_subtypes WHERE name = 'Cueva'`)
	e.area = one(`SELECT id FROM catalogue.areas WHERE name = 'Valdemanco' LIMIT 1`)
	return e
}

// place makes a complete cave (with a point, a way to get there, a description, a sheet and measures) with
// the visibility given.
func (e *env) place(name string, v places.Visibility) places.Place {
	e.t.Helper()
	ctx := context.Background()
	lat, lon, plat, plon := 40.85, -3.65, 40.86, -3.66
	text := func(s string) *string { return &s }
	depth, altitude := 30, 1100
	p, err := e.service.CreatePlace(ctx, e.by, places.Input{
		TypeID: e.typeID, SubtypeID: &e.subtype, Name: name, AreaID: e.area, Lat: &lat, Lon: &lon, State: "open",
		AltitudeM: &altitude, DepthM: &depth, Access: text("Por la pista de la derecha"),
		Description: text("Una cueva estrecha"), TechnicalSheet: text("P10, P20"),
		AltNames: []string{"La Boca"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.service.AddPoint(ctx, p.ID, e.by, places.PointInput{Kind: "parking", Name: text("El aparcamiento"), Lat: &plat, Lon: &plon}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.service.SetVisibility(ctx, p.ID, e.by, v); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) handler(limit int) http.Handler {
	// The authenticator shares the resolver the tokens were made in.
	authenticator := auth.NewAuthenticator(e.resolve)
	mux := http.NewServeMux()
	publicplaces.NewHandler(e.service, authenticator, limit, time.Minute).RegisterRoutes(mux)
	return mux
}

func get(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func vis(place, location, content places.Audience) places.Visibility {
	return places.Visibility{Place: place, Location: location, Content: content}
}

func asMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

const club, public = places.AudienceClub, places.AudiencePublic

// What an anonymous reader gets of a place depends on its visibility, level by level: a closed place is not
// there (as a place that does not exist is not), one open at the level of the place has its name, zone and
// measures and nothing more, and the location and the content come with their own levels.
func TestAnonymousSeesOnlyWhatIsPublic(t *testing.T) {
	e := newEnv(t)
	closed := e.place("Cueva cerrada", vis(club, club, club))
	onlyPlace := e.place("Cueva a medias", vis(public, club, club))
	located := e.place("Cueva con localización", vis(public, public, club))
	open := e.place("Cueva abierta", vis(public, public, public))
	h := e.handler(0)

	path := func(p places.Place) string { return "/api/v1/public/places/" + itoa(p.ID) }

	// The closed one: the same answer as a place that does not exist, and not in the list.
	rec := get(h, path(closed), "")
	missing := get(h, "/api/v1/public/places/987654321", "")
	if rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
		t.Errorf("a closed place must look like one that is not there: %d %q vs %d %q", rec.Code, rec.Body.String(), missing.Code, missing.Body.String())
	}
	list := get(h, "/api/v1/public/places", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	byName := map[string]map[string]any{}
	for _, it := range items {
		names = append(names, it["name"].(string))
		byName[it["name"].(string)] = it
	}
	if slices.Contains(names, "Cueva cerrada") || !slices.Contains(names, "Cueva abierta") || len(items) != 3 {
		t.Errorf("the list has the three open places and not the closed one: %v", names)
	}
	if _, ok := byName["Cueva a medias"]["coordinates"]; ok {
		t.Error("a place whose location is closed has no coordinates in the list")
	}
	if c, ok := byName["Cueva con localización"]["coordinates"].(map[string]any); !ok || c["lat"] != 40.85 {
		t.Errorf("a place whose location is open has them: %v", byName["Cueva con localización"]["coordinates"])
	}

	// The place alone: name, zone and measures, and no location, no marker, no content.
	got := asMap(t, get(h, path(onlyPlace), ""))
	if got["name"] != "Cueva a medias" || got["state"] != "open" || got["type"] != "Cavidad" || got["subtype"] != "Cueva" {
		t.Errorf("the place itself: %v", got)
	}
	for _, key := range []string{"coordinates", "location", "content"} {
		if _, ok := got[key]; ok {
			t.Errorf("a place open only at the level of the place has no %s: %v", key, got)
		}
	}
	if zones, _ := got["zones"].([]any); len(zones) < 2 || !strings.Contains(got["where"].(string), "Valdemanco") {
		t.Errorf("it says where it is, by zone: %v %v", got["where"], got["zones"])
	}
	if m, _ := got["measures"].(map[string]any); m["depth_m"] != float64(30) {
		t.Errorf("its measures: %v", got["measures"])
	}

	// The location open: the coordinate, the points and the way there; the content still closed.
	got = asMap(t, get(h, path(located), ""))
	loc, _ := got["location"].(map[string]any)
	if got["coordinates"] == nil || loc["access"] != "Por la pista de la derecha" || len(loc["points"].([]any)) != 1 {
		t.Errorf("an open location: %v", got)
	}
	if _, ok := got["content"]; ok {
		t.Errorf("the content is closed: %v", got)
	}

	// Everything open.
	got = asMap(t, get(h, path(open), ""))
	content, _ := got["content"].(map[string]any)
	if content["description"] != "Una cueva estrecha" || content["technical_sheet"] != "P10, P20" {
		t.Errorf("an open content: %v", got)
	}
}

// Whatever the place has, the answer is only what is written in the projection: never who wrote it, the
// activities, the element of OpenStreetMap, the visibility, the files, the system or any id of the club's.
func TestTheAnswerNeverHasWhatIsNotPublic(t *testing.T) {
	e := newEnv(t)
	open := e.place("Cueva abierta", vis(public, public, public))
	h := e.handler(0)
	allowedPlace := []string{"alt_names", "content", "coordinates", "id", "location", "measures", "name", "state", "subtype", "type", "where", "zone_ids", "zones"}
	for _, token := range []string{"", e.member} {
		got := asMap(t, get(h, "/api/v1/public/places/"+itoa(open.ID), token))
		for _, key := range keys(got) {
			if !slices.Contains(allowedPlace, key) {
				t.Errorf("token %q: %q must not be in the answer", token, key)
			}
		}
		list := get(h, "/api/v1/public/places", token).Body.String()
		for _, forbidden := range []string{"created_by", "updated_by", "created_at", "activity_count", "osm_", "visibility", "withheld", "file_count", "system", "katiuskas", "account"} {
			if strings.Contains(list, forbidden) || strings.Contains(get(h, "/api/v1/public/places/"+itoa(open.ID), token).Body.String(), forbidden) {
				t.Errorf("token %q: %q must not be anywhere in the answers", token, forbidden)
			}
		}
	}
}

// A member of the club reads the same routes and gets what the club sees (a closed place too), still
// without what is never public; a token that does not hold is refused and never gets the public answer.
func TestAMemberSeesMoreAndABadTokenIsRefused(t *testing.T) {
	e := newEnv(t)
	closed := e.place("Cueva cerrada", vis(club, club, club))
	h := e.handler(0)

	rec := get(h, "/api/v1/public/places/"+itoa(closed.ID), e.member)
	if rec.Code != http.StatusOK {
		t.Fatalf("the club sees its closed place: %d %s", rec.Code, rec.Body.String())
	}
	got := asMap(t, rec)
	if got["coordinates"] == nil || got["content"] == nil || got["location"] == nil {
		t.Errorf("a member sees every part: %v", got)
	}
	if rec := get(h, "/api/v1/public/places/"+itoa(closed.ID), "not-a-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("a bad token: %d, want 401", rec.Code)
	}
	if rec := get(h, "/api/v1/public/places", "not-a-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("a bad token on the list: %d, want 401", rec.Code)
	}
	if rec := get(h, "/api/v1/public/places/abc", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("an id that is not a number: %d, want 400", rec.Code)
	}
}

// What anybody may see can be kept by a cache for a minute; what a signed-in person sees never is.
func TestCacheHeaders(t *testing.T) {
	e := newEnv(t)
	e.place("Cueva abierta", vis(public, club, club))
	h := e.handler(0)
	anon := get(h, "/api/v1/public/places", "")
	if cc := anon.Header().Get("Cache-Control"); !strings.HasPrefix(cc, "public, max-age=60") {
		t.Errorf("anonymous: Cache-Control %q", cc)
	}
	if !strings.Contains(anon.Header().Get("Vary"), "Authorization") {
		t.Errorf("the answer depends on who asks: Vary %q", anon.Header().Get("Vary"))
	}
	signed := get(h, "/api/v1/public/places", e.member)
	if cc := signed.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("signed in: Cache-Control %q", cc)
	}
}

// Scraping the catalogue is made costly: an address may ask only so often.
func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	e.place("Cueva abierta", vis(public, club, club))
	h := e.handler(3)
	for i := 0; i < 3; i++ {
		if rec := get(h, "/api/v1/public/places", ""); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	rec := get(h, "/api/v1/public/places/1", "")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Errorf("the fourth request of an address in the window: %d %s", rec.Code, rec.Body.String())
	}
	// Another address has its own budget.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/places", nil)
	req.RemoteAddr = "192.0.2.77:1"
	other := httptest.NewRecorder()
	h.ServeHTTP(other, req)
	if other.Code != http.StatusOK {
		t.Errorf("another address: %d", other.Code)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

package osmmirror_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

type listAnswer struct {
	Elements  []osmmirror.Point `json:"elements"`
	Truncated bool              `json:"truncated"`
}

func serve(t *testing.T) (get func(path, token string) *httptest.ResponseRecorder, token string, repo *osmmirror.Repository) {
	t.Helper()
	tx := testutil.NewTx(t)
	repo = osmmirror.NewRepository(tx)
	authenticator, resolver := testutil.NewTestAuthenticator()
	mux := http.NewServeMux()
	osmmirror.NewHandler(osmmirror.NewService(repo), authenticator).RegisterRoutes(mux)
	user := testutil.InsertMember(t, tx)
	token = testutil.TokenFor(resolver, user.ID, "member")
	get = func(path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	return get, token, repo
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error struct{ Code string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != status || body.Error.Code != code {
		t.Errorf("expected %d %s, got %d %s", status, code, rec.Code, rec.Body.String())
	}
}

func at(e osmmirror.Element, lat, lon float64) osmmirror.Element {
	e.Lat, e.Lon = lat, lon
	return e
}

// The map asks for the elements in a box: those not gone, of the categories asked for, the club's own
// activity first, with a note when there were more than it gives.
func TestListElements(t *testing.T) {
	get, token, repo := serve(t)
	ctx := context.Background()
	canyon := osmmirror.Element{Type: "relation", ID: 7, Version: 1, Category: "canyon", Lat: 41.2, Lon: -0.3,
		Tags: map[string]string{"route": "canyoning", "name": "Barranco"}}
	if _, err := repo.Apply(ctx, "spain", []osmmirror.Element{
		at(hut(3), 40.1, -1.9), at(cave(1, "Cueva A"), 40.2, -1.8), at(cave(2, "Cueva B"), 41.5, -3.0), canyon,
		at(cave(5, "Lejos"), -33.0, 151.0), at(cave(6, "Borrada"), 40.15, -1.85),
	}); err != nil {
		t.Fatal(err)
	}
	// 6 disappears in a later load.
	if _, err := repo.Apply(ctx, "spain", []osmmirror.Element{
		at(hut(3), 40.1, -1.9), at(cave(1, "Cueva A"), 40.2, -1.8), at(cave(2, "Cueva B"), 41.5, -3.0), canyon,
		at(cave(5, "Lejos"), -33.0, 151.0),
	}); err != nil {
		t.Fatal(err)
	}

	list := func(q string) listAnswer {
		rec := get("/api/v1/osm/elements?"+q, token)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		var a listAnswer
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	box := "bbox=" + url.QueryEscape("40,-2,42,0")
	a := list(box)
	// In the box: cave 1, the hut 3 and the canyon 7; the cave 2 and 5 are outside, 6 is gone. Caves first.
	if len(a.Elements) != 3 || a.Truncated || a.Elements[0].ID != 1 || a.Elements[0].Name != "Cueva A" ||
		a.Elements[1].Category != "canyon" || a.Elements[2].Category != "hut" {
		t.Errorf("unexpected elements: %+v", a)
	}
	if got := list(box + "&category=hut"); len(got.Elements) != 1 || got.Elements[0].Type != "way" {
		t.Errorf("only the huts: %+v", got)
	}
	if got := list(box + "&category=cave,canyon"); len(got.Elements) != 2 {
		t.Errorf("caves and canyons: %+v", got)
	}
	// A box across the antimeridian: west greater than east.
	if got := list("bbox=" + url.QueryEscape("-40,150,-30,-170")); len(got.Elements) != 1 || got.Elements[0].ID != 5 {
		t.Errorf("a box across the antimeridian: %+v", got)
	}
	if got := list("bbox=" + url.QueryEscape("-10,-10,-5,-5")); got.Elements == nil || len(got.Elements) != 0 {
		t.Errorf("an empty box gives an empty list, not null: %+v", got)
	}

	errorCode(t, get("/api/v1/osm/elements", token), 400, "invalid_bbox")
	for _, bad := range []string{"1,2,3", "a,b,c,d", "42,-2,40,0", "-91,0,0,0", "0,-181,0,0", "NaN,0,1,1"} {
		errorCode(t, get("/api/v1/osm/elements?bbox="+url.QueryEscape(bad), token), 400, "invalid_bbox")
	}
	errorCode(t, get("/api/v1/osm/elements?"+box+"&category=pizza", token), 400, "invalid_osm_category")
	if rec := get("/api/v1/osm/elements?"+box, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

// More than MaxElements in a box are cut, and the answer says so.
func TestListTruncates(t *testing.T) {
	get, token, repo := serve(t)
	var many []osmmirror.Element
	for i := 1; i <= osmmirror.MaxElements+10; i++ {
		many = append(many, at(cave(int64(i), "C"), 40.0, -3.0))
	}
	if _, err := repo.Apply(context.Background(), "spain", many); err != nil {
		t.Fatal(err)
	}
	rec := get("/api/v1/osm/elements?bbox="+url.QueryEscape("39,-4,41,-2"), token)
	var a listAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || len(a.Elements) != osmmirror.MaxElements || !a.Truncated {
		t.Errorf("expected %d elements and truncated: %d %v %v", osmmirror.MaxElements, len(a.Elements), a.Truncated, err)
	}
}

// One element with all its data, and the errors of a wrong type, a wrong id and a missing element.
func TestGetElement(t *testing.T) {
	get, token, repo := serve(t)
	if _, err := repo.Apply(context.Background(), "spain", []osmmirror.Element{hut(3)}); err != nil {
		t.Fatal(err)
	}
	rec := get("/api/v1/osm/elements/way/3", token)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var d osmmirror.Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Type != "way" || d.ID != 3 || d.Version != 2 || d.Category != "hut" || d.Tags["tourism"] != "wilderness_hut" ||
		len(d.Geometry) == 0 || string(d.Geometry) == "null" || d.DeletedAt != nil {
		t.Errorf("unexpected element: %+v", d)
	}
	errorCode(t, get("/api/v1/osm/elements/node/3", token), 404, "not_found")
	errorCode(t, get("/api/v1/osm/elements/road/3", token), 400, "invalid_osm_element")
	errorCode(t, get("/api/v1/osm/elements/way/x", token), 400, "invalid_id")
	if rec := get("/api/v1/osm/elements/way/3", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

// The elements near a point that are not yet a place: those whose name looks like the one asked first (the
// same name, then a similar one), then the nearest, only inside the circle and of the categories asked for.
func TestNearby(t *testing.T) {
	get, token, repo := serve(t)
	// 0.001 degrees of latitude are about 111 metres.
	if _, err := repo.Apply(context.Background(), "spain", []osmmirror.Element{
		at(cave(1, "Cueva del Agua"), 40.0010, -3.0),   // ~111 m
		at(cave(2, "Sima Grande"), 40.0020, -3.0),      // ~222 m
		at(cave(3, "Cueva Del Águila"), 40.0030, -3.0), // ~334 m: another cave
		at(cave(4, "Cueva del agua"), 40.0040, -3.0),   // ~445 m: the same name as 1, ignoring case
		at(cave(5, "Lejana"), 40.0100, -3.0),           // ~1.1 km
		at(hut(6), 40.0005, -3.0),                      // a hut, ~55 m
	}); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		Elements []osmmirror.Candidate `json:"elements"`
	}
	nearby := func(q string) answer {
		rec := get("/api/v1/osm/nearby?lat=40&lon=-3&"+q, token)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		var a answer
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}

	// Without a name: the nearest first, inside the 500 m by default (the cave at 1.1 km is out).
	a := nearby("")
	ids := func(a answer) []int64 {
		var out []int64
		for _, c := range a.Elements {
			out = append(out, c.ID)
		}
		return out
	}
	if got := ids(a); len(got) != 5 || got[0] != 6 || got[1] != 1 || got[2] != 2 || got[3] != 3 || got[4] != 4 {
		t.Errorf("the nearest first: %v", got)
	}
	if a.Elements[1].DistanceM < 105 || a.Elements[1].DistanceM > 117 {
		t.Errorf("about 111 metres expected: %d", a.Elements[1].DistanceM)
	}

	// With a name: the exact matches first (1 and 4, the nearest of them first), then the rest by distance
	// (3 is "Águila", another word, so it does not look like the name).
	a = nearby("name=" + url.QueryEscape("Cueva del Agua"))
	if got := ids(a); got[0] != 1 || got[1] != 4 {
		t.Errorf("the same name first: %v", got)
	}
	if a.Elements[0].NameMatch != "exact" || a.Elements[1].NameMatch != "exact" {
		t.Errorf("exact matches expected: %+v", a.Elements[:2])
	}
	for _, c := range a.Elements[2:] {
		if c.NameMatch != "" {
			t.Errorf("only the first two look like the name: %+v", c)
		}
	}
	// A name that holds the other, or shares a word that is not generic, is similar.
	a = nearby("name=" + url.QueryEscape("Sima Grande de la Peña"))
	if a.Elements[0].ID != 2 || a.Elements[0].NameMatch != "similar" {
		t.Errorf("a similar name first: %+v", a.Elements[0])
	}
	a = nearby("name=" + url.QueryEscape("Torca Grande"))
	if a.Elements[0].ID != 2 || a.Elements[0].NameMatch != "similar" {
		t.Errorf("a shared word that is not generic: %+v", a.Elements[0])
	}
	a = nearby("name=" + url.QueryEscape("Cueva Perdida"))
	for _, c := range a.Elements {
		if c.NameMatch != "" {
			t.Errorf("a generic word is not a match: %+v", c)
		}
	}

	// The radius and the categories.
	if got := ids(nearby("radius=150")); len(got) != 2 || got[0] != 6 || got[1] != 1 {
		t.Errorf("radius of 150 m: %v", got)
	}
	if got := ids(nearby("radius=2000")); len(got) != 6 {
		t.Errorf("radius of 2 km: %v", got)
	}
	if got := ids(nearby("category=hut")); len(got) != 1 || got[0] != 6 {
		t.Errorf("only huts: %v", got)
	}

	errorCode(t, get("/api/v1/osm/nearby?lat=40&lon=-3&radius=0x", token), 400, "invalid_radius")
	errorCode(t, get("/api/v1/osm/nearby?lat=40&lon=-3&radius=5000", token), 400, "invalid_radius")
	errorCode(t, get("/api/v1/osm/nearby?lat=40&lon=-3&radius=-5", token), 400, "invalid_radius")
	errorCode(t, get("/api/v1/osm/nearby?lat=x&lon=-3", token), 400, "invalid_bbox")
	errorCode(t, get("/api/v1/osm/nearby?lat=95&lon=-3", token), 400, "invalid_bbox")
	errorCode(t, get("/api/v1/osm/nearby?lat=40&lon=-3&category=pizza", token), 400, "invalid_osm_category")
	if rec := get("/api/v1/osm/nearby?lat=40&lon=-3", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

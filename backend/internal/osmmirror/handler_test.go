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

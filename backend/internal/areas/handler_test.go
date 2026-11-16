package areas_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
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

type person struct {
	user  testutil.TestUser
	token string
}

// fakeBoundaries answers what a source of boundaries would: what it is set to, or an error.
type fakeBoundaries struct {
	answer areas.Containing
	err    error
	calls  int
}

func (f *fakeBoundaries) Containing(context.Context, float64, float64) (areas.Containing, error) {
	f.calls++
	return f.answer, f.err
}

// fakeGeocoder answers what a geocoder would: what it is set to, or an error, and says what it was asked.
type fakeGeocoder struct {
	answer []areas.Place
	err    error
	asked  []string
}

func (f *fakeGeocoder) Search(_ context.Context, text string) ([]areas.Place, error) {
	f.asked = append(f.asked, text)
	return f.answer, f.err
}

type env struct {
	boundaries *fakeBoundaries
	geocoder   *fakeGeocoder
	// bare serves the same routes of a service with no source of boundaries.
	bare     *http.ServeMux
	t        *testing.T
	tx       pgx.Tx
	mux      *http.ServeMux
	notifier *fakeNotifier
	resolver testutil.FakeResolver
	// who: a plain member, board and an admin.
	member, board, admin person
	// the seeded zones the tests use.
	spain, madridCommunity, madrid, valdemanco, portugal int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	boundaries := &fakeBoundaries{}
	geocoder := &fakeGeocoder{}
	areas.NewHandler(areas.NewService(areas.NewRepository(tx)).WithBoundaries(boundaries).WithGeocoder(geocoder), authenticator, usersService, notifier).RegisterRoutes(mux)
	bare := http.NewServeMux()
	areas.NewHandler(areas.NewService(areas.NewRepository(tx)), authenticator, usersService, notifier).RegisterRoutes(bare)
	e := &env{t: t, tx: tx, mux: mux, bare: bare, notifier: notifier, resolver: resolver, boundaries: boundaries, geocoder: geocoder}
	e.member = e.person("member")
	e.board = e.person("board")
	e.admin = e.person("admin")
	one := func(query string, args ...any) int64 {
		var id int64
		if err := tx.QueryRow(context.Background(), query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.spain = one(`SELECT id FROM areas WHERE parent_id IS NULL AND code = 'ES'`)
	e.portugal = one(`SELECT id FROM areas WHERE parent_id IS NULL AND code = 'PT'`)
	e.madridCommunity = one(`SELECT id FROM areas WHERE parent_id = $1 AND name = 'Comunidad de Madrid'`, e.spain)
	e.madrid = one(`SELECT id FROM areas WHERE parent_id = $1 AND name = 'Madrid'`, e.madridCommunity)
	e.valdemanco = one(`SELECT id FROM areas WHERE parent_id = $1 AND name = 'Valdemanco'`, e.madrid)
	return e
}

func (e *env) person(roles ...string) person {
	u := testutil.InsertMember(e.t, e.tx)
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

// add creates a zone below another as the member and returns it.
func (e *env) add(parent int64, name, kind string) areas.Area {
	e.t.Helper()
	rec := e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": parent, "name": name, "kind": kind})
	status(e.t, rec, http.StatusCreated)
	return decode[areas.Area](e.t, rec)
}

// placeIn puts a place in a zone, straight in the database (the places have their own tests).
func (e *env) placeIn(area int64) {
	e.t.Helper()
	tag, err := e.tx.Exec(context.Background(), `
		INSERT INTO places (type_id, kind, has_state, state, subtype_id, name, area_id, lat, lon, created_by, updated_by)
		SELECT t.id, t.kind, t.has_state, 'open', (SELECT id FROM place_subtypes WHERE name = 'Cueva'), 'Un lugar',
			$1, 40, -3, $2, $2
		FROM place_types t WHERE t.name = 'Cavidad'
	`, area, e.member.user.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		e.t.Fatalf("expected one place to be inserted, got %d", tag.RowsAffected()) // not silently none
	}
}

func areaPath(a areas.Area) string { return fmt.Sprintf("/api/v1/areas/%d", a.ID) }

// The zones the migration seeds: the states of the United Nations and, for Spain, the communities, the
// provinces and the municipalities of the INE, each below the right one; and what they are called in the
// normalized form is what Go says (the migration is generated by a script in Python).
func TestSeed(t *testing.T) {
	e := newEnv(t)
	count := func(query string, args ...any) int {
		var n int
		if err := e.tx.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM areas WHERE parent_id IS NULL`); n != 195 {
		t.Errorf("expected the 195 states of the UN (193 members and 2 observers), got %d", n)
	}
	// The territories of ISO 3166-1 that are not states are not roots, and the observers are.
	for _, code := range []string{"GI", "HK", "PR", "GL", "TW", "EH", "AQ", "FO", "AX", "XK"} {
		if n := count(`SELECT count(*) FROM areas WHERE parent_id IS NULL AND code = $1`, code); n != 0 {
			t.Errorf("%s is not a state of the UN and should not be a country", code)
		}
	}
	for _, code := range []string{"ES", "PT", "FR", "AD", "MA", "VA", "PS", "CI", "TR", "CN", "NL", "GM", "NR"} {
		if n := count(`SELECT count(*) FROM areas WHERE parent_id IS NULL AND code = $1`, code); n != 1 {
			t.Errorf("%s should be a country", code)
		}
	}
	if n := count(`SELECT count(*) FROM areas WHERE parent_id IS NULL AND (kind <> 'País' OR code !~ '^[A-Z]{2}$')`); n != 0 {
		t.Errorf("%d roots are not a country with an ISO code", n)
	}
	if n := count(`SELECT count(*) FROM areas WHERE parent_id = $1 AND kind IN ('Comunidad autónoma', 'Ciudad autónoma')`, e.spain); n != 19 {
		t.Errorf("expected the 19 communities and cities of Spain, got %d", n)
	}
	if n := count(`SELECT count(*) FROM areas p JOIN areas c ON c.id = p.parent_id WHERE p.kind = 'Provincia' AND c.parent_id = $1`, e.spain); n != 52 {
		t.Errorf("expected the 52 provinces, each in a community, got %d", n)
	}
	if n := count(`SELECT count(*) FROM areas WHERE kind = 'Municipio'`); n < 8100 || n > 8200 {
		t.Errorf("expected about 8,130 municipalities, got %d", n)
	}
	if n := count(`SELECT count(*) FROM areas m JOIN areas p ON p.id = m.parent_id WHERE m.kind = 'Municipio' AND (p.kind <> 'Provincia' OR substr(m.code, 1, 2) <> p.code OR m.code !~ '^[0-9]{5}$')`); n != 0 {
		t.Errorf("%d municipalities are not in their own province, or have no INE code", n)
	}
	if n := count(`SELECT count(*) FROM areas WHERE parent_id IS NOT NULL AND parent_id NOT IN (SELECT id FROM areas)`); n != 0 {
		t.Errorf("%d zones have a parent that is not there", n)
	}
	// The article the INE writes last is first again, and the two languages stay together.
	for name, parent := range map[string]string{"A Coruña": "A Coruña", "La Vila Joiosa/Villajoyosa": "Alicante/Alacant", "L'Hospitalet de Llobregat": "Barcelona"} {
		if n := count(`SELECT count(*) FROM areas m JOIN areas p ON p.id = m.parent_id WHERE m.kind = 'Municipio' AND m.name = $1 AND p.name = $2`, name, parent); n != 1 {
			t.Errorf("expected the municipality %q in %q", name, parent)
		}
	}
	// norm_name is what Normalize gives, for every zone.
	rows, err := e.tx.Query(context.Background(), `SELECT name, norm_name FROM areas`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	bad := 0
	for rows.Next() {
		var name, normalized string
		if err := rows.Scan(&name, &normalized); err != nil {
			t.Fatal(err)
		}
		if got := areas.Normalize(name); got != normalized {
			if bad++; bad <= 5 {
				t.Errorf("Normalize(%q) = %q, the seed has %q", name, got, normalized)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d zones whose normalized name differs", bad)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Peña":                         "pena",
		"Donostia/San Sebastián":       "donostia san sebastian",
		"  L'Hospitalet  de Llobregat": "l hospitalet de llobregat",
		"Côte d’Ivoire":                "cote d ivoire",
		"Łódź Ålesund Øye Straße":      "lodz alesund oye strasse",
		"Zaragoza":                     "zaragoza",
		"---":                          "",
		"":                             "",
		"Vila-real (Castelló)":         "vila real castello",
	} {
		if got := areas.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// Everybody who can sign in reads the zones and adds one; renaming, merging and deleting are for admin
// alone; and a role that is not on top of a baseline one is refused everywhere.
func TestPermissions(t *testing.T) {
	e := newEnv(t)
	victim := e.add(e.madrid, "Municipio de prueba", "")
	other := e.add(e.madrid, "Otro municipio de prueba", "")
	lonely := testutil.TokenFor(e.resolver, e.member.user.ID, "material")

	type call struct {
		method, path string
		body         any
	}
	readers := []call{
		{"GET", "/api/v1/areas", nil},
		{"GET", fmt.Sprintf("/api/v1/areas?parent_id=%d", e.madrid), nil},
		{"GET", "/api/v1/areas?search=madrid", nil},
		{"GET", areaPath(victim), nil},
	}
	adders := []call{{"POST", "/api/v1/areas", map[string]any{"parent_id": e.madrid, "name": "Otro más"}}}
	admins := []call{
		{"PUT", areaPath(victim), map[string]any{"name": "Renombrado"}},
		{"POST", areaPath(victim) + "/merge", map[string]any{"into_id": other.ID}},
		{"DELETE", areaPath(other), nil},
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
	allowed("member", e.member.token, adders)
	refused("member", e.member.token, admins)
	refused("board", e.board.token, admins)
	refused("a role without member, board or admin", lonely, append(append(readers, adders...), admins...))
	if rec := e.json("GET", "/api/v1/areas", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: expected 401, got %d", rec.Code)
	}
	allowed("admin", e.admin.token, admins)
}

// The countries, the zones below one, and the search: by name ignoring case, accents and punctuation,
// with the zones each one is in.
func TestListAndSearch(t *testing.T) {
	e := newEnv(t)
	roots := decode[[]areas.Area](t, e.json("GET", "/api/v1/areas", e.member.token, nil))
	if len(roots) != 195 || roots[0].ParentID != nil || roots[0].ChildCount != 0 {
		t.Fatalf("expected the 195 states of the UN: %d", len(roots))
	}
	for i := 1; i < len(roots); i++ {
		if areas.Normalize(roots[i-1].Name) > areas.Normalize(roots[i].Name) {
			t.Errorf("the countries should be by name: %q before %q", roots[i-1].Name, roots[i].Name)
		}
	}
	children := decode[[]areas.Area](t, e.json("GET", fmt.Sprintf("/api/v1/areas?parent_id=%d", e.spain), e.member.token, nil))
	if len(children) != 19 || children[0].Name != "Andalucía" {
		t.Errorf("the communities of Spain: %d, the first %q", len(children), children[0].Name)
	}
	for _, c := range children {
		if c.Name == "Comunidad de Madrid" && c.ChildCount != 1 {
			t.Errorf("Madrid is one province: %+v", c)
		}
	}
	madrid := decode[areas.Area](t, e.json("GET", fmt.Sprintf("/api/v1/areas/%d", e.madrid), e.member.token, nil))
	if madrid.ChildCount < 150 || len(madrid.Ancestors) != 2 || madrid.Ancestors[0].Name != "España" || madrid.Ancestors[1].Name != "Comunidad de Madrid" {
		t.Errorf("unexpected province: %+v", madrid)
	}
	leaf := decode[[]areas.Area](t, e.json("GET", fmt.Sprintf("/api/v1/areas?parent_id=%d", e.valdemanco), e.member.token, nil))
	if len(leaf) != 0 {
		t.Errorf("a municipality has nothing below: %+v", leaf)
	}
	errorCode(t, e.json("GET", "/api/v1/areas?parent_id=987654321", e.member.token, nil), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/areas?parent_id=abc", e.member.token, nil), 400, "invalid_id")
	errorCode(t, e.json("GET", "/api/v1/areas/987654321", e.member.token, nil), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/areas/abc", e.member.token, nil), 400, "invalid_id")

	search := func(q string) []areas.Area {
		return decode[[]areas.Area](t, e.json("GET", "/api/v1/areas?search="+url.QueryEscape(q), e.member.token, nil))
	}
	found := search("valdemanco")
	if len(found) == 0 || found[0].ID != e.valdemanco || len(found[0].Ancestors) != 3 || found[0].Ancestors[2].Name != "Madrid" {
		t.Errorf("Valdemanco and what it is in: %+v", found)
	}
	for _, q := range []string{"VALDEMANCO", "  valdemanco  ", "Valdémanco", "valdeman"} {
		if got := search(q); len(got) == 0 || got[0].ID != e.valdemanco {
			t.Errorf("%q should find Valdemanco first: %+v", q, got)
		}
	}
	names := func(list []areas.Area) []string {
		var out []string
		for _, a := range list {
			out = append(out, a.Name)
		}
		return out
	}
	if got := names(search("coruna")); len(got) == 0 || !strings.Contains(strings.Join(got, "|"), "A Coruña") {
		t.Errorf("without the tilde: %v", got)
	}
	if got := names(search("donostia san sebastian")); len(got) == 0 || got[0] != "Donostia/San Sebastián" {
		t.Errorf("punctuation does not count: %v", got)
	}
	// An exact name first, then those that start with it, then those that contain it.
	madrids := names(search("madrid"))
	if len(madrids) < 3 || madrids[0] != "Madrid" || len(search("madrid")) > areas.DefaultSearchLimit {
		t.Errorf("exact first, and a limit: %v", madrids)
	}
	if len(search("")) != 0 || len(search("---")) != 0 {
		t.Error("an empty search finds nothing")
	}
	if got := decode[[]areas.Area](t, e.json("GET", "/api/v1/areas?search=san&limit=3", e.member.token, nil)); len(got) != 3 {
		t.Errorf("the limit: %d", len(got))
	}
	if got := decode[[]areas.Area](t, e.json("GET", "/api/v1/areas?search=san&limit=100000", e.member.token, nil)); len(got) != areas.MaxSearchLimit {
		t.Errorf("the most a search gives: %d", len(got))
	}
}

// A zone is added below another, by anybody: its kind is what the zones already below it have, and with
// none it has to be said; the name is unique below each zone ignoring case, accents and punctuation.
func TestAdd(t *testing.T) {
	e := newEnv(t)
	a := e.add(e.madrid, "Municipio Nuevo", "")
	if a.Kind != "Municipio" || a.Name != "Municipio Nuevo" || a.ChildCount != 0 || a.PlaceCount != 0 || len(a.Ancestors) != 3 || a.ParentID == nil || *a.ParentID != e.madrid {
		t.Errorf("unexpected zone: %+v", a)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido la zona «Municipio Nuevo» (Municipio) en «Madrid»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// Below a country with nothing below it, the kind is given, and then it is the kind of the siblings.
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.portugal, "name": "Aveiro"}), 400, "area_kind_required")
	aveiro := e.add(e.portugal, "Aveiro", "Distrito")
	e.notifier.wait(t)
	if aveiro.Kind != "Distrito" {
		t.Errorf("the kind given: %+v", aveiro)
	}
	if next := e.add(e.portugal, "Beja", ""); next.Kind != "Distrito" {
		t.Errorf("the kind of its siblings: %+v", next)
	}
	// A zone of another level can be added too, with its kind (a different one below the same zone).
	if parish := e.add(aveiro.ID, "Freguesia de prueba", "Freguesia"); len(parish.Ancestors) != 2 {
		t.Errorf("below a district: %+v", parish)
	}

	// The same name is the same zone, whatever its case, accents and punctuation; below another zone it
	// is another one.
	for _, name := range []string{"municipio nuevo", "MUNICIPIO  NUEVO", "Munícipio-Nuevo"} {
		errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.madrid, "name": name}), 409, "duplicate_area")
	}
	var comunidad int64
	if err := e.tx.QueryRow(context.Background(), `SELECT id FROM areas WHERE parent_id = $1 AND name = 'Murcia'`, e.spain).Scan(&comunidad); err != nil {
		// Murcia is a community under a longer name; any other zone does as well.
		comunidad = e.madridCommunity
	}
	status(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": comunidad, "name": "Municipio Nuevo", "kind": "Zona"}), http.StatusCreated)

	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.madrid, "name": "   "}), 400, "area_name_required")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.madrid, "name": "---"}), 400, "area_name_required")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.madrid, "name": strings.Repeat("a", areas.MaxNameLength+1)}), 400, "area_text_too_long")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": e.madrid, "name": "X", "kind": strings.Repeat("a", areas.MaxKindLength+1)}), 400, "area_text_too_long")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"name": "Sin padre"}), 403, "area_root_admin_only")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": 0, "name": "Sin padre"}), 403, "area_root_admin_only")
	errorCode(t, e.json("POST", "/api/v1/areas", e.member.token, map[string]any{"parent_id": 987654321, "name": "Huérfana"}), 400, "unknown_area")
}

// An admin renames a zone and changes its kind, but not into the name of another below the same one.
func TestRename(t *testing.T) {
	e := newEnv(t)
	a := e.add(e.madrid, "Municipio Viejo", "")
	e.add(e.madrid, "Municipio Ocupado", "")
	e.notifier.wait(t)
	e.notifier.wait(t)
	path := areaPath(a)
	rec := e.json("PUT", path, e.admin.token, map[string]any{"name": "  Municipio Renovado ", "kind": "Localidad"})
	status(t, rec, http.StatusOK)
	got := decode[areas.Area](t, rec)
	if got.Name != "Municipio Renovado" || got.Kind != "Localidad" || got.ID != a.ID {
		t.Errorf("unexpected zone: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha cambiado la zona «Municipio Viejo» (Municipio) por «Municipio Renovado» (Localidad)") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// Without a kind, it stays; only the case changing is the same zone.
	got = decode[areas.Area](t, e.json("PUT", path, e.admin.token, map[string]any{"name": "MUNICIPIO RENOVADO"}))
	if got.Kind != "Localidad" || got.Name != "MUNICIPIO RENOVADO" {
		t.Errorf("the kind should stay: %+v", got)
	}
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "municipio ocupado"}), 409, "duplicate_area")
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": "  "}), 400, "area_name_required")
	errorCode(t, e.json("PUT", "/api/v1/areas/987654321", e.admin.token, map[string]any{"name": "X"}), 404, "not_found")
	// A country can be renamed.
	status(t, e.json("PUT", fmt.Sprintf("/api/v1/areas/%d", e.portugal), e.admin.token, map[string]any{"name": "República Portuguesa"}), http.StatusOK)
}

// A zone is deleted only if nothing is below it and no place is in it, and a country never is.
func TestDelete(t *testing.T) {
	e := newEnv(t)
	leaf := e.add(e.madrid, "Hoja", "")
	parent := e.add(e.madrid, "Rama", "")
	e.add(parent.ID, "Hijo", "Barrio")
	used := e.add(e.madrid, "Con lugar", "")
	e.placeIn(used.ID)
	errorCode(t, e.json("DELETE", areaPath(parent), e.admin.token, nil), 409, "area_has_children")
	errorCode(t, e.json("DELETE", areaPath(used), e.admin.token, nil), 409, "area_in_use")
	errorCode(t, e.json("DELETE", fmt.Sprintf("/api/v1/areas/%d", e.spain), e.admin.token, nil), 409, "area_is_country")
	errorCode(t, e.json("DELETE", "/api/v1/areas/987654321", e.admin.token, nil), 404, "not_found")
	for i := 0; i < 4; i++ {
		e.notifier.wait(t)
	}
	status(t, e.json("DELETE", areaPath(leaf), e.admin.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha borrado la zona «Hoja» (Municipio)") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("GET", areaPath(leaf), e.member.token, nil), 404, "not_found")
}

// Merging moves the zones below a zone and its places to another below the same one, and deletes it.
func TestMerge(t *testing.T) {
	e := newEnv(t)
	from := e.add(e.madrid, "Pueblo Duplicado", "")
	into := e.add(e.madrid, "Pueblo", "")
	e.add(from.ID, "Barrio Alto", "Barrio")
	e.placeIn(from.ID)
	e.notifier.wait(t)
	e.notifier.wait(t)
	e.notifier.wait(t)

	rec := e.json("POST", areaPath(from)+"/merge", e.admin.token, map[string]any{"into_id": into.ID})
	status(t, rec, http.StatusOK)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha fusionado la zona «Pueblo Duplicado» en «Pueblo» (Municipio)") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("GET", areaPath(from), e.member.token, nil), 404, "not_found")
	merged := decode[areas.Area](t, e.json("GET", areaPath(into), e.member.token, nil))
	if merged.ChildCount != 1 || merged.PlaceCount != 1 {
		t.Errorf("what was in the first should be in the second: %+v", merged)
	}

	// What is refused: itself, a zone below another one, a country, one that does not exist, and a zone
	// whose children have the name of one of the target's.
	other := e.add(e.madridCommunity, "En otro sitio", "Provincia")
	errorCode(t, e.json("POST", areaPath(into)+"/merge", e.admin.token, map[string]any{"into_id": into.ID}), 400, "area_merge_self")
	errorCode(t, e.json("POST", areaPath(into)+"/merge", e.admin.token, map[string]any{"into_id": other.ID}), 409, "area_merge_parents")
	errorCode(t, e.json("POST", fmt.Sprintf("/api/v1/areas/%d/merge", e.spain), e.admin.token, map[string]any{"into_id": e.portugal}), 409, "area_is_country")
	errorCode(t, e.json("POST", areaPath(into)+"/merge", e.admin.token, map[string]any{"into_id": 987654321}), 404, "not_found")
	errorCode(t, e.json("POST", "/api/v1/areas/987654321/merge", e.admin.token, map[string]any{"into_id": into.ID}), 404, "not_found")
	a := e.add(e.madrid, "Pueblo A", "")
	b := e.add(e.madrid, "Pueblo B", "")
	e.add(a.ID, "Plaza Mayor", "Barrio")
	e.add(b.ID, "plaza mayor", "Barrio")
	errorCode(t, e.json("POST", areaPath(a)+"/merge", e.admin.token, map[string]any{"into_id": b.ID}), 409, "duplicate_area")
	if still := decode[areas.Area](t, e.json("GET", areaPath(a), e.member.token, nil)); still.ChildCount != 1 {
		t.Errorf("a refused merge changes nothing: %+v", still)
	}
}

// Only an admin adds a country, a zone that is in no other one; its kind is "País" unless it is said.
func TestAddCountry(t *testing.T) {
	e := newEnv(t)
	rec := e.json("POST", "/api/v1/areas", e.admin.token, map[string]any{"name": "Atlántida"})
	status(t, rec, http.StatusCreated)
	got := decode[areas.Area](t, rec)
	if got.ParentID != nil || got.Kind != "País" || len(got.Ancestors) != 0 {
		t.Errorf("unexpected country: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido la zona «Atlántida» (País), que no está dentro de ninguna otra") {
		t.Errorf("unexpected notice: %q", msg)
	}
	status(t, e.json("POST", "/api/v1/areas", e.admin.token, map[string]any{"name": "Mu", "kind": "Continente perdido"}), http.StatusCreated)
	e.notifier.wait(t)
	errorCode(t, e.json("POST", "/api/v1/areas", e.admin.token, map[string]any{"name": "atlantida"}), 409, "duplicate_area")
	errorCode(t, e.json("POST", "/api/v1/areas", e.admin.token, map[string]any{"name": " "}), 400, "area_name_required")
	// And it is a place for the zones below.
	e.add(got.ID, "Capital", "Ciudad")
}

// An admin moves a zone below another, or makes it a country; never into itself or below itself, and
// not under the name of another zone there.
func TestMove(t *testing.T) {
	e := newEnv(t)
	a := e.add(e.madrid, "Municipio Movido", "")
	child := e.add(a.ID, "Barrio", "Barrio")
	e.notifier.wait(t)
	e.notifier.wait(t)
	path := areaPath(a)

	// Below another zone, with its zones coming along.
	rec := e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "parent_id": e.portugal})
	status(t, rec, http.StatusOK)
	got := decode[areas.Area](t, rec)
	if got.ParentID == nil || *got.ParentID != e.portugal || len(got.Ancestors) != 1 || got.ChildCount != 1 {
		t.Errorf("unexpected zone: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha movido la zona «Municipio Movido» (Municipio) de «Madrid» a «Portugal»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	moved := decode[areas.Area](t, e.json("GET", areaPath(child), e.member.token, nil))
	if len(moved.Ancestors) != 2 || moved.Ancestors[0].ID != e.portugal {
		t.Errorf("a zone below it should have moved too: %+v", moved)
	}

	// Without parent_id it stays where it is; only the kind changes.
	got = decode[areas.Area](t, e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "kind": "Otro"}))
	if *got.ParentID != e.portugal || got.Kind != "Otro" {
		t.Errorf("it should not have moved: %+v", got)
	}
	e.notifier.wait(t)

	// Into itself, or below a zone that is below it.
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "parent_id": a.ID}), 409, "area_move_cycle")
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "parent_id": child.ID}), 409, "area_move_cycle")
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "parent_id": 987654321}), 400, "unknown_area")
	errorCode(t, e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "parent_id": "uno"}), 400, "invalid_body")

	// Under the name of another zone there; nothing changes, the rename included.
	e.add(e.madrid, "Municipio Ocupado", "")
	e.notifier.wait(t)
	occupied := e.add(e.portugal, "Municipio Ocupado", "")
	e.notifier.wait(t)
	errorCode(t, e.json("PUT", areaPath(occupied), e.admin.token, map[string]any{"name": "Municipio Ocupado", "parent_id": e.madrid}), 409, "duplicate_area")
	if still := decode[areas.Area](t, e.json("GET", areaPath(occupied), e.member.token, nil)); *still.ParentID != e.portugal {
		t.Errorf("a refused move must change nothing: %+v", still)
	}

	// A country can be put below another zone, and a zone made a country with null.
	status(t, e.json("PUT", fmt.Sprintf("/api/v1/areas/%d", e.portugal), e.admin.token, map[string]any{"name": "Portugal", "parent_id": e.spain}), http.StatusOK)
	e.notifier.wait(t)
	rec = e.json("PUT", path, e.admin.token, map[string]any{"name": a.Name, "kind": "País", "parent_id": nil})
	status(t, rec, http.StatusOK)
	got = decode[areas.Area](t, rec)
	if got.ParentID != nil || len(got.Ancestors) != 0 {
		t.Errorf("it should be a country now: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "(Otro) por «Municipio Movido» (País)") {
		t.Errorf("unexpected notice: %q", msg)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha sacado la zona «Municipio Movido» (País) de «Portugal»: ya no está dentro de ninguna otra") {
		t.Errorf("unexpected notice: %q", msg)
	}

	// Members cannot.
	errorCode(t, e.json("PUT", path, e.member.token, map[string]any{"name": a.Name, "parent_id": e.spain}), 403, "forbidden")
}

// The zone of a point is proposed from what the source of boundaries says: the municipality by its INE
// code, else the country, else none; and a failure of the source is told, not hidden.
func TestLocate(t *testing.T) {
	e := newEnv(t)
	locate := func(query string) *httptest.ResponseRecorder {
		return e.json("GET", "/api/v1/areas/locate?"+query, e.member.token, nil)
	}
	type answer struct {
		Area *areas.Area `json:"area"`
	}
	var valdemancoCode string
	if err := e.tx.QueryRow(context.Background(), `SELECT code FROM areas WHERE id = $1`, e.valdemanco).Scan(&valdemancoCode); err != nil {
		t.Fatal(err)
	}

	e.boundaries.answer = areas.Containing{CountryCode: "ES", MunicipalityCode: valdemancoCode}
	rec := locate("lat=40.85&lon=-3.62")
	status(t, rec, http.StatusOK)
	got := decode[answer](t, rec)
	if got.Area == nil || got.Area.ID != e.valdemanco || len(got.Area.Ancestors) != 3 {
		t.Errorf("the municipality, with its ancestors: %+v", got.Area)
	}

	// A municipality the seed does not have: the country.
	e.boundaries.answer = areas.Containing{CountryCode: "PT", MunicipalityCode: "99999"}
	if got := decode[answer](t, locate("lat=39&lon=-8")); got.Area == nil || got.Area.ID != e.portugal {
		t.Errorf("the country: %+v", got.Area)
	}

	// Nothing known: no zone, which is not an error.
	e.boundaries.answer = areas.Containing{}
	rec = locate("lat=0&lon=-30")
	status(t, rec, http.StatusOK)
	if got := decode[answer](t, rec); got.Area != nil {
		t.Errorf("no zone expected: %+v", got.Area)
	}

	// The source is down: 502, with its own code.
	e.boundaries.err = areas.ErrLocateUnavailable
	errorCode(t, locate("lat=40&lon=-3"), 502, "locate_unavailable")
	e.boundaries.err = nil

	// Not coordinates: refused before asking anybody.
	calls := e.boundaries.calls
	for _, query := range []string{"", "lat=40", "lat=x&lon=1", "lat=91&lon=0", "lat=0&lon=181", "lat=NaN&lon=0"} {
		errorCode(t, locate(query), 400, "invalid_coordinates")
	}
	if e.boundaries.calls != calls {
		t.Errorf("an invalid point must not be asked for")
	}
	// Anybody who can sign in, nobody else.
	if rec := e.json("GET", "/api/v1/areas/locate?lat=1&lon=1", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}

	// Without a source configured there is nothing to propose.
	req := httptest.NewRequest("GET", "/api/v1/areas/locate?lat=40&lon=-3", nil)
	req.Header.Set("Authorization", "Bearer "+e.member.token)
	rec = httptest.NewRecorder()
	e.bare.ServeHTTP(rec, req)
	status(t, rec, http.StatusOK)
	if got := decode[answer](t, rec); got.Area != nil {
		t.Errorf("no source, no zone: %+v", got.Area)
	}
}

// Searching a name for a map gives what the geocoder finds; blank or too long text is not sent, a failure
// of the source is told, and without a source there is nothing found.
func TestGeocode(t *testing.T) {
	e := newEnv(t)
	search := func(q string) *httptest.ResponseRecorder {
		return e.json("GET", "/api/v1/areas/geocode?q="+url.QueryEscape(q), e.member.token, nil)
	}
	e.geocoder.answer = []areas.Place{{Name: "Valdemanco, Madrid", Lat: 40.87, Lon: -3.63,
		Bounds: &areas.Bounds{South: 40.8, West: -3.7, North: 40.9, East: -3.6}}}
	rec := search("  valdemanco ")
	status(t, rec, http.StatusOK)
	got := decode[[]areas.Place](t, rec)
	if len(got) != 1 || got[0].Name != "Valdemanco, Madrid" || got[0].Bounds == nil || got[0].Bounds.North != 40.9 {
		t.Errorf("unexpected places: %+v", got)
	}
	if len(e.geocoder.asked) != 1 || e.geocoder.asked[0] != "valdemanco" {
		t.Errorf("the text is asked trimmed, got %q", e.geocoder.asked)
	}

	// Blank: an empty list, and nobody is asked.
	rec = search("   ")
	status(t, rec, http.StatusOK)
	if got := decode[[]areas.Place](t, rec); len(got) != 0 || len(e.geocoder.asked) != 1 {
		t.Errorf("blank finds nothing and asks nobody: %+v, %q", got, e.geocoder.asked)
	}
	errorCode(t, search(strings.Repeat("x", areas.MaxSearchLength+1)), 400, "area_text_too_long")

	e.geocoder.err = areas.ErrSearchUnavailable
	errorCode(t, search("madrid"), 502, "search_unavailable")
	e.geocoder.err = nil

	if rec := e.json("GET", "/api/v1/areas/geocode?q=madrid", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/areas/geocode?q=madrid", nil)
	req.Header.Set("Authorization", "Bearer "+e.member.token)
	rec = httptest.NewRecorder()
	e.bare.ServeHTTP(rec, req)
	status(t, rec, http.StatusOK)
	if got := decode[[]areas.Place](t, rec); len(got) != 0 {
		t.Errorf("no source, nothing found: %+v", got)
	}
}

package publicsite_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/publicsite"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

const (
	siteURL = "https://cuevasysimas.es"
	host    = "cuevasysimas.es"
)

var completeLegal = publicsite.Legal{Name: "Proyecto Cuevas y simas", Address: "Calle Falsa 1, Madrid", Email: "hola@cuevasysimas.es"}

type noActivities struct{}

func (noActivities) ActivityCounts(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}
func (noActivities) HasActivities(context.Context, int64) (bool, error) { return false, nil }

type env struct {
	t       *testing.T
	service *places.Service
	by      int64
	typeID  int64
	subtype int64
	area    int64
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
	user := testutil.NewPerson(t)
	e := &env{t: t, service: service, by: testutil.CatalogueAccount(t, tx, user)}
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

func vis(place, location, content places.Audience) places.Visibility {
	return places.Visibility{Place: place, Location: location, Content: content}
}

const club, public = places.AudienceClub, places.AudiencePublic

func (e *env) place(name string, v places.Visibility) places.Place {
	e.t.Helper()
	ctx := context.Background()
	lat, lon := 40.85, -3.65
	text := func(s string) *string { return &s }
	depth := 30
	p, err := e.service.CreatePlace(ctx, e.by, places.Input{
		TypeID: e.typeID, SubtypeID: &e.subtype, Name: name, AreaID: e.area, Lat: &lat, Lon: &lon, State: "permit_required",
		DepthM: &depth, Access: text("Por la pista de la derecha"),
		Description: text("Una cueva **estrecha**.\n\n- primera sala\n- segunda sala"), TechnicalSheet: text("P10, P20"),
		AltNames: []string{"La Boca"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.service.SetVisibility(ctx, p.ID, e.by, v); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) site(legal publicsite.Legal, limit int) http.Handler {
	h, err := publicsite.NewHandler(e.service, siteURL, legal, limit, time.Minute)
	if err != nil {
		e.t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func get(h http.Handler, hostName, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://"+hostName+path, nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTheSiteAnswersOnlyItsOwnHost(t *testing.T) {
	e := newEnv(t)
	e.place("Cueva abierta", vis(public, club, club))
	h := e.site(completeLegal, 0)
	if rec := get(h, host, "/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Cueva abierta") {
		t.Fatalf("its own host: %d", rec.Code)
	}
	for _, other := range []string{"intranet.katiuskas.es", "localhost:64711", "evil.example"} {
		if rec := get(h, other, "/"); rec.Code != http.StatusNotFound {
			t.Errorf("host %s: %d, want 404", other, rec.Code)
		}
		if rec := get(h, other, "/sitemap.xml"); rec.Code != http.StatusNotFound {
			t.Errorf("host %s sitemap: %d, want 404", other, rec.Code)
		}
	}
}

// A site on another port, such as a local one, answers to its name whatever the port.
func TestASiteOnAPort(t *testing.T) {
	e := newEnv(t)
	h, err := publicsite.NewHandler(e.service, "http://localhost:64720", completeLegal, 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if rec := get(mux, "localhost:64720", "/"); rec.Code != http.StatusOK {
		t.Errorf("the site on its port: %d", rec.Code)
	}
	if rec := get(mux, "other.example:64720", "/"); rec.Code != http.StatusNotFound {
		t.Errorf("another host on the same port: %d, want 404", rec.Code)
	}
}

func TestTheSiteIsOffWithoutAnAddressAndRefusesABadOne(t *testing.T) {
	e := newEnv(t)
	off, err := publicsite.NewHandler(e.service, "", completeLegal, 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	off.RegisterRoutes(mux)
	if rec := get(mux, host, "/"); rec.Code != http.StatusNotFound {
		t.Errorf("a site with no address answers nothing: %d", rec.Code)
	}
	for _, bad := range []string{"cuevasysimas.es", "ftp://cuevasysimas.es", "https://cuevasysimas.es/ruta", "https://"} {
		if _, err := publicsite.NewHandler(e.service, bad, completeLegal, 0, time.Minute); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// What a visitor sees is what the public routes say: a closed place is not in the list, the sitemap or the
// pages (it is the same page as one that is not there), and each part appears with its level.
func TestPagesShowOnlyWhatIsPublic(t *testing.T) {
	e := newEnv(t)
	closed := e.place("Cueva cerrada", vis(club, club, club))
	onlyPlace := e.place("Cueva a medias", vis(public, club, club))
	located := e.place("Cueva con localización", vis(public, public, club))
	open := e.place("Cueva abierta", vis(public, public, public))
	h := e.site(completeLegal, 0)
	path := func(p places.Place) string { return "/lugares/" + slugAndID(p) }

	home := get(h, host, "/").Body.String()
	for _, name := range []string{"Cueva a medias", "Cueva con localización", "Cueva abierta"} {
		if !strings.Contains(home, name) {
			t.Errorf("the home page must list %q", name)
		}
	}
	if strings.Contains(home, "Cueva cerrada") {
		t.Error("a closed place is not in the list")
	}
	sitemap := get(h, host, "/sitemap.xml").Body.String()
	if strings.Contains(sitemap, "cerrada") || !strings.Contains(sitemap, siteURL+path(open)) || !strings.Contains(sitemap, "<loc>"+siteURL+"/</loc>") {
		t.Errorf("the sitemap has the open places and not the closed one: %s", sitemap)
	}

	rec, missing := get(h, host, path(closed)), get(h, host, "/lugares/no-existe-987654")
	if rec.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
		t.Errorf("a closed place is the page of one that is not there: %d vs %d", rec.Code, missing.Code)
	}
	if strings.Contains(rec.Body.String(), "cerrada") {
		t.Error("the page of a closed place says nothing of it")
	}

	body := get(h, host, path(onlyPlace)).Body.String()
	for _, want := range []string{"Cueva a medias", "Hace falta permiso", "Valdemanco", "30 m", "La Boca"} {
		if !strings.Contains(body, want) {
			t.Errorf("the place itself must say %q", want)
		}
	}
	for _, not := range []string{"40.85", "openstreetmap.org/?mlat", "Por la pista", "estrecha", "P10"} {
		if strings.Contains(body, not) {
			t.Errorf("a place open only at the level of the place must not say %q", not)
		}
	}

	body = get(h, host, path(located)).Body.String()
	if !strings.Contains(body, "40.85000, -3.65000") || !strings.Contains(body, "https://www.openstreetmap.org/?mlat=40.85000&amp;mlon=-3.65000") || !strings.Contains(body, "Por la pista de la derecha") {
		t.Errorf("an open location has the coordinates, a link to OpenStreetMap and the way there: %s", body)
	}
	if strings.Contains(body, "estrecha") || strings.Contains(body, "P10") {
		t.Error("the content is closed")
	}

	body = get(h, host, path(open)).Body.String()
	if !strings.Contains(body, "<strong>estrecha</strong>") || !strings.Contains(body, "<li>primera sala</li>") || !strings.Contains(body, "P10, P20") {
		t.Errorf("an open content, formatted: %s", body)
	}
	for _, not := range []string{"created", "updated", "activity", "katiuskas", "visibility", "osm_", "<script"} {
		if strings.Contains(strings.ToLower(body), not) {
			t.Errorf("a page must not have %q", not)
		}
	}
}

// slugAndID is how the site addresses a place: its name and, at the end, its number.
func slugAndID(p places.Place) string {
	return strings.TrimPrefix(placeAddress(p), "/lugares/")
}

func placeAddress(p places.Place) string {
	slug := strings.ToLower(p.Name)
	r := strings.NewReplacer(" ", "-", "ó", "o", "á", "a", "é", "e", "í", "i", "ú", "u", "ñ", "n")
	return "/lugares/" + r.Replace(slug) + "-" + itoa(p.ID)
}

func itoa(id int64) string {
	const digits = "0123456789"
	if id == 0 {
		return "0"
	}
	var b []byte
	for id > 0 {
		b = append([]byte{digits[id%10]}, b...)
		id /= 10
	}
	return string(b)
}

// The address says the name, but it is the number that finds the place: another spelling of the name goes,
// permanently, to the current one.
func TestAPlaceHasOneAddress(t *testing.T) {
	e := newEnv(t)
	p := e.place("Sima de la Peña", vis(public, club, club))
	h := e.site(completeLegal, 0)
	canonical := "/lugares/sima-de-la-pena-" + itoa(p.ID)
	if rec := get(h, host, canonical); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<link rel="canonical" href="`+siteURL+canonical+`">`) {
		t.Fatalf("the canonical address: %d", rec.Code)
	}
	for _, other := range []string{"/lugares/" + itoa(p.ID), "/lugares/nombre-antiguo-" + itoa(p.ID), "/lugares/SIMA-de-la-pena-" + itoa(p.ID)} {
		rec := get(h, host, other)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != canonical {
			t.Errorf("%s: %d %q, want a permanent redirect to %s", other, rec.Code, rec.Header().Get("Location"), canonical)
		}
	}
	for _, bad := range []string{"/lugares/sima", "/lugares/sima-0", "/lugares/sima-abc"} {
		if rec := get(h, host, bad); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", bad, rec.Code)
		}
	}
}

// A name that is HTML is text.
func TestWhatPeopleTypeIsEscaped(t *testing.T) {
	e := newEnv(t)
	p := e.place(`<script>alert("x")</script>`, vis(public, public, public))
	h := e.site(completeLegal, 0)
	for _, path := range []string{"/", "/lugares/script-alert-x-script-" + itoa(p.ID), "/?q=%3Cscript%3E"} {
		body := get(h, host, path).Body.String()
		if strings.Contains(body, "<script>") || strings.Contains(body, `alert("x")`) {
			t.Errorf("%s: markup got through", path)
		}
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	e.place("Sima de la Peña", vis(public, club, club))
	e.place("Cueva del Agua", vis(public, club, club))
	h := e.site(completeLegal, 0)
	body := get(h, host, "/?q=pena").Body.String()
	if !strings.Contains(body, "Sima de la Peña") || strings.Contains(body, "Cueva del Agua") || !strings.Contains(body, "1 de 2 lugares") {
		t.Errorf("a search ignores accents and case and finds one: %s", body)
	}
	if !strings.Contains(body, `<meta name="robots" content="noindex">`) {
		t.Error("a search is not a page to index")
	}
	if body := get(h, host, "/?q=zzz").Body.String(); !strings.Contains(body, "No hay ningún lugar") {
		t.Errorf("nothing found: %s", body)
	}
	if body := get(h, host, "/").Body.String(); strings.Contains(body, `content="noindex"`) {
		t.Error("the home page is indexed once the legal data is complete")
	}
}

// Until somebody answers for the site, the pages say so and keep the search engines out.
func TestLegalDataPending(t *testing.T) {
	e := newEnv(t)
	e.place("Cueva abierta", vis(public, club, club))
	pending := e.site(publicsite.Legal{Name: "Proyecto"}, 0)
	home := get(pending, host, "/").Body.String()
	if !strings.Contains(home, `<meta name="robots" content="noindex">`) {
		t.Error("with the legal data incomplete the site asks the engines to stay out")
	}
	if robots := get(pending, host, "/robots.txt").Body.String(); !strings.Contains(robots, "Disallow: /\n") || strings.Contains(robots, "Sitemap") {
		t.Errorf("robots.txt: %q", robots)
	}
	if notice := get(pending, host, "/aviso-legal").Body.String(); !strings.Contains(notice, "pendientes de completar") {
		t.Errorf("the legal notice says it is pending: %s", notice)
	}

	done := e.site(completeLegal, 0)
	robots := get(done, host, "/robots.txt").Body.String()
	if !strings.Contains(robots, "Sitemap: "+siteURL+"/sitemap.xml") || !strings.Contains(robots, "Disallow: /api/") || strings.Contains(robots, "Disallow: /\n") {
		t.Errorf("robots.txt: %q", robots)
	}
	for path, want := range map[string]string{"/aviso-legal": "Calle Falsa 1, Madrid", "/privacidad": "hola@cuevasysimas.es"} {
		body := get(done, host, path).Body.String()
		if !strings.Contains(body, want) || strings.Contains(body, "pendientes de completar") || strings.Contains(body, `content="noindex"`) {
			t.Errorf("%s with the legal data: %s", path, body)
		}
	}
	if body := get(done, host, "/privacidad").Body.String(); !strings.Contains(body, "no usa cookies") {
		t.Error("the privacy policy says there are no cookies")
	}
}

func TestHeadersAndStylesheet(t *testing.T) {
	e := newEnv(t)
	e.place("Cueva abierta", vis(public, club, club))
	h := e.site(completeLegal, 0)
	rec := get(h, host, "/")
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Cache-Control":          "public, max-age=60, stale-while-revalidate=300",
		"Content-Type":           "text/html; charset=utf-8",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s: %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") || strings.Contains(csp, "unsafe") {
		t.Errorf("a page runs no script: %q", csp)
	}
	if !strings.Contains(rec.Body.String(), "<html lang=\"es\">") || !strings.Contains(rec.Body.String(), "OpenStreetMap") {
		t.Error("the page is in Spanish and credits OpenStreetMap")
	}
	if strings.Contains(rec.Body.String(), "<script") || strings.Contains(strings.ToLower(rec.Header().Get("Set-Cookie")), "=") {
		t.Error("no script and no cookie")
	}
	css := get(h, host, "/static/site.css")
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") || !strings.Contains(css.Body.String(), "--accent") {
		t.Errorf("the stylesheet: %d %q", css.Code, css.Header().Get("Content-Type"))
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	h := e.site(completeLegal, 2)
	get(h, host, "/")
	get(h, host, "/")
	if rec := get(h, host, "/"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the third request of an address: %d, want 429", rec.Code)
	}
}

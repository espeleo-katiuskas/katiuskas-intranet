// Package publicsite is the public site of the catalogue of places, «Cuevas y simas»: pages that a search
// engine can read, made on the server from the same projection as the public routes
// (internal/publicplaces), so that they say what those say and nothing more. It shows the places as
// anybody may see them: there is no session here, and a member of the club sees the same as anybody.
//
// The site answers only the host of its own address (PUBLIC_SITE_URL, https://cuevasysimas.es): the
// routes are registered for that host (the router ignores the port), so on the intranet's they do not
// exist. With no address the
// site is off. There is not a script on a page, nor a cookie, nor a map of tiles of anybody's: where a
// place may show its coordinates it links to OpenStreetMap, which is not embedded.
package publicsite

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/publicplaces"
)

//go:embed templates/*.html static/*
var files embed.FS

// SiteName is what the site is called.
const SiteName = "Cuevas y simas"

// Legal is who answers for the site, which the legal notice and the privacy policy say. Until it is
// complete the pages say it is pending and the site asks the search engines to stay out.
type Legal struct {
	Name    string
	Address string
	Email   string
}

// Complete says whether the three are given.
func (l Legal) Complete() bool {
	return strings.TrimSpace(l.Name) != "" && strings.TrimSpace(l.Address) != "" && strings.TrimSpace(l.Email) != ""
}

// Catalogue is what the site asks of the places.
type Catalogue = publicplaces.Catalogue

type Handler struct {
	catalogue Catalogue
	base      *url.URL
	legal     Legal
	limiter   *httpapi.IPRateLimiter
	pages     map[string]*template.Template
	now       func() time.Time
}

// NewHandler makes the site at the address given, such as https://cuevasysimas.es: with none the site is
// off, and it is an error if it is not an address with a scheme and a host. Each address of a visitor may
// ask at most `limit` times per `window`.
func NewHandler(catalogue Catalogue, siteURL string, legal Legal, limit int, window time.Duration) (*Handler, error) {
	h := &Handler{catalogue: catalogue, legal: legal, now: time.Now, limiter: httpapi.NewIPRateLimiter(limit, window)}
	if strings.TrimSpace(siteURL) == "" {
		return h, nil
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(siteURL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return nil, fmt.Errorf("the address of the public site must be like https://cuevasysimas.es, not %q", siteURL)
	}
	h.base = u
	funcs := template.FuncMap{
		"rich":        renderRichText,
		"stateLabel":  stateLabel,
		"stateHelp":   stateHelp,
		"placeURL":    func(id int64, name string) string { return placePath(id, name) },
		"osmLink":     osmLink,
		"coordinates": formatCoordinates,
	}
	h.pages = map[string]*template.Template{}
	for _, name := range []string{"home", "place", "notice", "privacy", "notfound"} {
		t, err := template.New("layout").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		h.pages[name] = t
	}
	return h, nil
}

// RegisterRoutes registers the pages for the host of the site, and nothing if the site is off.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	if h.base == nil {
		return
	}
	// The router matches the host without its port, so a site on another port (a local one, for trying it)
	// answers to the name alone.
	host := h.base.Hostname()
	mux.Handle("GET "+host+"/{$}", h.route(h.home))
	mux.Handle("GET "+host+"/lugares/{slug}", h.route(h.place))
	mux.Handle("GET "+host+"/sitemap.xml", h.route(h.sitemap))
	mux.Handle("GET "+host+"/robots.txt", h.route(h.robots))
	mux.Handle("GET "+host+"/aviso-legal", h.route(h.page("notice", "Aviso legal")))
	mux.Handle("GET "+host+"/privacidad", h.route(h.page("privacy", "Política de privacidad")))
	mux.Handle("GET "+host+"/static/site.css", h.route(h.stylesheet))
}

func (h *Handler) route(next http.HandlerFunc) http.Handler {
	return h.limiter.Limit(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Nothing runs on a page and nothing is loaded from elsewhere.
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		next(w, r)
	})
}

// canonical is the address of a path of the site.
func (h *Handler) canonical(path string) string { return h.base.String() + path }

// page is what every template is given.
type page struct {
	SiteName    string
	Title       string
	Description string
	Canonical   string
	// NoIndex asks the search engines to stay out, while the legal data is not complete.
	NoIndex bool
	Legal   Legal
	Year    int

	// Home.
	Query  string
	Places []row
	Total  int

	// Place.
	Place *publicplaces.Place

	// The legal pages.
	LegalComplete bool
}

// row is a place of the list of the home page.
type row struct {
	Name, Type, Where, URL string
	State                  places.State
}

func (h *Handler) newPage(r *http.Request, title, description, path string) page {
	if !strings.HasPrefix(title, SiteName) {
		title += " — " + SiteName
	}
	return page{
		SiteName: SiteName, Title: title, Description: description, Canonical: h.canonical(path),
		NoIndex: !h.legal.Complete(), Legal: h.legal, LegalComplete: h.legal.Complete(), Year: h.now().Year(),
	}
}

// render writes a page with the status given. Pages may be kept by a cache for a minute.
func (h *Handler) render(w http.ResponseWriter, status int, name string, data page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status == http.StatusOK {
		w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	var b strings.Builder
	if err := h.pages[name].ExecuteTemplate(&b, "layout", data); err != nil {
		http.Error(w, "could not show the page", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	// The page says nothing of the address asked for, so that the page of a place that is closed and that of
	// one that is not there are the same.
	data := h.newPage(r, "No encontrado", "", "")
	data.Canonical = ""
	data.NoIndex = true
	h.render(w, http.StatusNotFound, "notfound", data)
}

// visible is the places that anybody may know exist.
func (h *Handler) visible(ctx context.Context) ([]publicplaces.ListItem, error) {
	items, err := h.catalogue.ListPlaces(ctx)
	if err != nil {
		return nil, err
	}
	return publicplaces.ViewList(items, access.Anonymous(), h.now()), nil
}

func typeLabel(p publicplaces.ListItem) string {
	if p.Subtype != nil && *p.Subtype != "" {
		return *p.Subtype
	}
	return p.Type
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	list, err := h.visible(r.Context())
	if err != nil {
		http.Error(w, "could not list the places", http.StatusInternalServerError)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	terms := strings.Fields(areas.Normalize(query))
	data := h.newPage(r, SiteName+": cuevas, simas y otros lugares", "Catálogo abierto de cuevas, simas y otros lugares subterráneos: dónde están, qué son y en qué estado.", "/")
	data.Query = query
	data.Total = len(list)
	for _, it := range list {
		haystack := areas.Normalize(it.Name + " " + strings.Join(it.AltNames, " ") + " " + it.Where + " " + typeLabel(it))
		match := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				match = false
				break
			}
		}
		if match {
			data.Places = append(data.Places, row{Name: it.Name, Type: typeLabel(it), Where: it.Where, State: it.State, URL: placePath(it.ID, it.Name)})
		}
	}
	if query != "" {
		// A search is not a page to index.
		data.NoIndex = true
	}
	h.render(w, http.StatusOK, "home", data)
}

func (h *Handler) place(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	id, ok := placeIDOf(slug)
	if !ok {
		h.notFound(w, r)
		return
	}
	p, err := h.catalogue.GetPlace(r.Context(), id)
	if err != nil {
		if errors.Is(err, places.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		http.Error(w, "could not read the place", http.StatusInternalServerError)
		return
	}
	// A place that is closed and one that is not there are the same page.
	seen, ok := publicplaces.ViewPlace(p, access.Anonymous(), h.now())
	if !ok {
		h.notFound(w, r)
		return
	}
	// The address says the name, which may change: any other spelling of it goes to the current one.
	if want := strings.TrimPrefix(placePath(seen.ID, seen.Name), "/lugares/"); slug != want {
		http.Redirect(w, r, placePath(seen.ID, seen.Name), http.StatusMovedPermanently)
		return
	}
	description := typeLabel(seen.ListItem) + " en " + seen.Where + "."
	if label := stateLabel(seen.State); label != "" {
		description += " " + label + "."
	}
	data := h.newPage(r, seen.Name, description, placePath(seen.ID, seen.Name))
	data.Place = &seen
	h.render(w, http.StatusOK, "place", data)
}

// page is a page that has only text, such as the legal notice.
func (h *Handler) page(name, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.render(w, http.StatusOK, name, h.newPage(r, title, "", r.URL.Path))
	}
}

func (h *Handler) stylesheet(w http.ResponseWriter, r *http.Request) {
	css, err := files.ReadFile("static/site.css")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(css)
}

func (h *Handler) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// While the legal data is pending the site is not offered to the search engines.
	if !h.legal.Complete() {
		fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
		return
	}
	fmt.Fprintf(w, "User-agent: *\nDisallow: /api/\nSitemap: %s\n", h.canonical("/sitemap.xml"))
}

func (h *Handler) sitemap(w http.ResponseWriter, r *http.Request) {
	list, err := h.visible(r.Context())
	if err != nil {
		http.Error(w, "could not list the places", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	loc := func(path string) {
		b.WriteString("  <url><loc>" + xmlEscape(h.canonical(path)) + "</loc></url>\n")
	}
	loc("/")
	for _, it := range list {
		loc(placePath(it.ID, it.Name))
	}
	b.WriteString("</urlset>\n")
	_, _ = w.Write([]byte(b.String()))
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// slugOf is the part of an address that says the name: lower case, without accents, with dashes.
func slugOf(name string) string {
	slug := strings.ReplaceAll(areas.Normalize(name), " ", "-")
	if slug == "" {
		return "lugar"
	}
	return slug
}

// placePath is the address of the page of a place: its name and, at the end, its number, which is what
// finds it (the name may change, and two places may have the same).
func placePath(id int64, name string) string {
	return "/lugares/" + slugOf(name) + "-" + strconv.FormatInt(id, 10)
}

// placeIDOf is the number at the end of the last part of an address of a place.
func placeIDOf(slug string) (int64, bool) {
	tail := slug
	if i := strings.LastIndexByte(slug, '-'); i >= 0 {
		tail = slug[i+1:]
	}
	id, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != tail {
		return 0, false
	}
	return id, true
}

func stateLabel(s places.State) string {
	switch s {
	case "open":
		return "Abierta"
	case "permit_required":
		return "Hace falta permiso"
	case "tourist":
		return "Turística (de pago)"
	case "closed":
		return "Cerrada"
	}
	return ""
}

func stateHelp(s places.State) string {
	switch s {
	case "open":
		return "Se puede ir libremente."
	case "permit_required":
		return "Hay que pedir permiso antes de ir."
	case "tourist":
		return "Está explotada y hay que pagar para visitarla."
	case "closed":
		return "No se puede visitar."
	}
	return ""
}

func formatCoordinates(lat, lon float64) string {
	return strconv.FormatFloat(lat, 'f', 5, 64) + ", " + strconv.FormatFloat(lon, 'f', 5, 64)
}

// osmLink is the address that opens a point in OpenStreetMap (a link, not a map of ours).
func osmLink(lat, lon float64) string {
	la, lo := strconv.FormatFloat(lat, 'f', 5, 64), strconv.FormatFloat(lon, 'f', 5, 64)
	return "https://www.openstreetmap.org/?mlat=" + la + "&mlon=" + lo + "#map=16/" + la + "/" + lo
}

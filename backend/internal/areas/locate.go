package areas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Where a point is, as a source of boundaries says it: the ISO 3166-1 alpha-2 code of its country and the
// INE code of its Spanish municipality (the first five digits of `ref:ine`), each empty when unknown.
type Containing struct {
	CountryCode      string
	MunicipalityCode string
}

// Boundaries says which administrative divisions contain a point.
type Boundaries interface {
	Containing(ctx context.Context, lat, lon float64) (Containing, error)
}

// ErrLocateUnavailable means the source of boundaries could not be asked or did not answer.
var ErrLocateUnavailable = errors.New("the zone of a point could not be looked up now")

// Nominatim asks a Nominatim instance (OpenStreetMap's geocoder) for the administrative division at a
// point, at the level of a municipality (`zoom=10`), with its extra tags: in Spain that division carries
// `ref:ine`. It answers in a fraction of a second, which Overpass, the other public service that could
// say it, does not when it is busy (half a minute, or not at all). Its usage policy asks for at most one
// request per second and a client that says who it is, so the requests are spaced out, the answers are
// kept for a day by the point rounded to about ten metres, and the name of the client is generic.
type Nominatim struct {
	url    string
	client *http.Client

	mu    sync.Mutex    // Guards next, cache and searches.
	next  time.Time     // When the next request may go out.
	gap   time.Duration // How long after a request the next may go out.
	cache map[string]cached
	// searches keeps the answers of Search by the text asked, as cache does for the points.
	searches map[string]searched
}

type searched struct {
	value   []Place
	expires time.Time
}

type cached struct {
	value   Containing
	expires time.Time
}

const (
	nominatimTimeout   = 8 * time.Second
	nominatimInterval  = time.Second // What the usage policy of the public instance asks for.
	nominatimCacheTTL  = 24 * time.Hour
	nominatimCacheSize = 2000
)

// NewNominatim uses the instance whose reverse endpoint is at url (".../reverse").
func NewNominatim(url string) *Nominatim {
	return &Nominatim{
		url: url, client: &http.Client{Timeout: nominatimTimeout}, cache: map[string]cached{},
		searches: map[string]searched{}, gap: nominatimInterval,
	}
}

// Spacing changes how long the requests are kept apart (for an instance of one's own, or a test).
func (n *Nominatim) Spacing(gap time.Duration) *Nominatim {
	n.gap = gap
	return n
}

func cacheKey(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

func (n *Nominatim) cached(key string) (Containing, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	hit, ok := n.cache[key]
	if !ok || time.Now().After(hit.expires) {
		delete(n.cache, key)
		return Containing{}, false
	}
	return hit.value, true
}

func (n *Nominatim) remember(key string, value Containing) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.cache) >= nominatimCacheSize {
		now := time.Now()
		for k, v := range n.cache {
			if now.After(v.expires) {
				delete(n.cache, k)
			}
		}
		if len(n.cache) >= nominatimCacheSize {
			n.cache = map[string]cached{} // Full of live entries: start again rather than grow.
		}
	}
	n.cache[key] = cached{value: value, expires: time.Now().Add(nominatimCacheTTL)}
}

// wait takes the next turn to send a request, at least nominatimInterval after the one before.
func (n *Nominatim) wait(ctx context.Context) error {
	n.mu.Lock()
	turn := n.next
	if now := time.Now(); turn.Before(now) {
		turn = now
	}
	if time.Until(turn) > nominatimTimeout {
		// So many are waiting that this one would give up anyway: say so now, and do not make the queue longer.
		n.mu.Unlock()
		return errors.New("too many requests waiting")
	}
	n.next = turn.Add(n.gap)
	n.mu.Unlock()
	select {
	case <-time.After(time.Until(turn)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type nominatimAnswer struct {
	// Error is what Nominatim says when there is nothing there (the sea): it is still a 200.
	Error   string `json:"error"`
	Address struct {
		CountryCode string `json:"country_code"`
	} `json:"address"`
	ExtraTags map[string]string `json:"extratags"`
}

// municipalityCode is the INE code of the municipality a `ref:ine` names: its first five digits.
func municipalityCode(ref string) string {
	if len(ref) < 5 {
		return ""
	}
	for _, r := range ref[:5] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return ref[:5]
}

func (n *Nominatim) Containing(ctx context.Context, lat, lon float64) (Containing, error) {
	key := cacheKey(lat, lon)
	if hit, ok := n.cached(key); ok {
		return hit, nil
	}
	if err := n.wait(ctx); err != nil {
		return Containing{}, fmt.Errorf("%w: %v", ErrLocateUnavailable, err)
	}
	query := url.Values{
		"format": {"jsonv2"}, "lat": {strconv.FormatFloat(lat, 'f', 6, 64)},
		"lon": {strconv.FormatFloat(lon, 'f', 6, 64)}, "zoom": {"10"},
		"extratags": {"1"}, "addressdetails": {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.url+"?"+query.Encode(), nil)
	if err != nil {
		return Containing{}, err
	}
	// A generic name: the instance asks who is calling, and nothing more than that is needed.
	req.Header.Set("User-Agent", "intranet-zone-lookup/1.0")
	resp, err := n.client.Do(req)
	if err != nil {
		return Containing{}, fmt.Errorf("%w: %v", ErrLocateUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Containing{}, fmt.Errorf("%w: status %d", ErrLocateUnavailable, resp.StatusCode)
	}
	var answer nominatimAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer); err != nil {
		return Containing{}, fmt.Errorf("%w: %v", ErrLocateUnavailable, err)
	}
	var found Containing
	if answer.Error == "" {
		if code := strings.ToUpper(answer.Address.CountryCode); len(code) == 2 {
			found.CountryCode = code
		}
		found.MunicipalityCode = municipalityCode(answer.ExtraTags["ref:ine"])
	}
	n.remember(key, found)
	return found, nil
}

// validPoint says whether a latitude and a longitude are numbers that fit WGS84.
func validPoint(lat, lon float64) bool {
	ok := func(v, limit float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= limit }
	return ok(lat, 90) && ok(lon, 180)
}

// ErrSearchUnavailable means the geocoder could not be asked or did not answer.
var ErrSearchUnavailable = errors.New("the search for a place could not be done now")

// Place is a result of searching a name: where it is, and the box that frames it (a town is bigger than a
// peak), as WGS84 degrees.
type Place struct {
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Bounds *Bounds `json:"bounds"`
}

// Bounds is a box of latitudes and longitudes.
type Bounds struct {
	South float64 `json:"south"`
	West  float64 `json:"west"`
	North float64 `json:"north"`
	East  float64 `json:"east"`
}

// Geocoder finds places by name.
type Geocoder interface {
	Search(ctx context.Context, text string) ([]Place, error)
}

const searchLimit = 6

type searchAnswer struct {
	DisplayName string   `json:"display_name"`
	Lat         string   `json:"lat"`
	Lon         string   `json:"lon"`
	BoundingBox []string `json:"boundingbox"` // south, north, west, east.
}

// searchURL is the "search" endpoint of the instance whose "reverse" one is n.url.
func (n *Nominatim) searchURL() string {
	return strings.TrimSuffix(n.url, "/reverse") + "/search"
}

func (n *Nominatim) cachedSearch(key string) ([]Place, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	hit, ok := n.searches[key]
	if !ok || time.Now().After(hit.expires) {
		delete(n.searches, key)
		return nil, false
	}
	return hit.value, true
}

func (n *Nominatim) rememberSearch(key string, value []Place) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.searches) >= nominatimCacheSize {
		n.searches = map[string]searched{}
	}
	n.searches[key] = searched{value: value, expires: time.Now().Add(nominatimCacheTTL)}
}

// Search is the places a text names, best first (OpenStreetMap's own ranking), in Spanish when it can
// be. Like Containing it is spaced out and kept for a day; only the text goes out, with a generic client.
func (n *Nominatim) Search(ctx context.Context, text string) ([]Place, error) {
	key := Normalize(text)
	if hit, ok := n.cachedSearch(key); ok {
		return hit, nil
	}
	if err := n.wait(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSearchUnavailable, err)
	}
	query := url.Values{
		"format": {"jsonv2"}, "q": {text}, "limit": {strconv.Itoa(searchLimit)}, "accept-language": {"es"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.searchURL()+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "intranet-zone-lookup/1.0")
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSearchUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrSearchUnavailable, resp.StatusCode)
	}
	var answers []searchAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answers); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSearchUnavailable, err)
	}
	found := []Place{}
	for _, a := range answers {
		lat, latErr := strconv.ParseFloat(a.Lat, 64)
		lon, lonErr := strconv.ParseFloat(a.Lon, 64)
		if latErr != nil || lonErr != nil || !validPoint(lat, lon) {
			continue
		}
		place := Place{Name: a.DisplayName, Lat: lat, Lon: lon}
		if len(a.BoundingBox) == 4 {
			var v [4]float64
			ok := true
			for i, s := range a.BoundingBox {
				var perr error
				if v[i], perr = strconv.ParseFloat(s, 64); perr != nil {
					ok = false
				}
			}
			if ok && validPoint(v[0], v[2]) && validPoint(v[1], v[3]) {
				place.Bounds = &Bounds{South: v[0], North: v[1], West: v[2], East: v[3]}
			}
		}
		found = append(found, place)
	}
	n.rememberSearch(key, found)
	return found, nil
}

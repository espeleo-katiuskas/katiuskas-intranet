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

// Overpass asks Overpass API instances (OpenStreetMap's data) for the boundaries that contain a point,
// the next one when an instance is busy or does not answer: the public ones are often at capacity. The
// answers are kept for a day, by the point rounded to about ten metres, and no more than a few
// requests are in flight at once, as the public instances ask for light use.
type Overpass struct {
	urls   []string
	client *http.Client
	slots  chan struct{}

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	value   Containing
	expires time.Time
}

const (
	overpassTimeout   = 6 * time.Second // For each instance.
	overpassCacheTTL  = 24 * time.Hour
	overpassCacheSize = 2000
	overpassInFlight  = 3
)

// NewOverpass uses the instances at the endpoints (".../api/interpreter"), in order.
func NewOverpass(endpoints ...string) *Overpass {
	return &Overpass{
		urls:   endpoints,
		client: &http.Client{Timeout: overpassTimeout},
		slots:  make(chan struct{}, overpassInFlight),
		cache:  map[string]cached{},
	}
}

func cacheKey(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

func (o *Overpass) cached(key string) (Containing, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	hit, ok := o.cache[key]
	if !ok || time.Now().After(hit.expires) {
		delete(o.cache, key)
		return Containing{}, false
	}
	return hit.value, true
}

func (o *Overpass) remember(key string, value Containing) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.cache) >= overpassCacheSize {
		now := time.Now()
		for k, v := range o.cache {
			if now.After(v.expires) {
				delete(o.cache, k)
			}
		}
		if len(o.cache) >= overpassCacheSize {
			o.cache = map[string]cached{} // Full of live entries: start again rather than grow.
		}
	}
	o.cache[key] = cached{value: value, expires: time.Now().Add(overpassCacheTTL)}
}

type overpassAnswer struct {
	Elements []struct {
		Tags map[string]string `json:"tags"`
	} `json:"elements"`
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

func (o *Overpass) Containing(ctx context.Context, lat, lon float64) (Containing, error) {
	key := cacheKey(lat, lon)
	if hit, ok := o.cached(key); ok {
		return hit, nil
	}
	select {
	case o.slots <- struct{}{}:
		defer func() { <-o.slots }()
	case <-ctx.Done():
		return Containing{}, ErrLocateUnavailable
	}
	query := fmt.Sprintf(`[out:json][timeout:5];is_in(%.6f,%.6f)->.a;area.a[boundary=administrative][admin_level~"^(2|8)$"];out tags;`, lat, lon)
	var answer overpassAnswer
	var lastErr error
	for _, endpoint := range o.urls {
		if lastErr = o.ask(ctx, endpoint, query, &answer); lastErr == nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr != nil {
		return Containing{}, fmt.Errorf("%w: %v", ErrLocateUnavailable, lastErr)
	}
	var found Containing
	for _, e := range answer.Elements {
		if code := strings.ToUpper(e.Tags["ISO3166-1"]); len(code) == 2 && found.CountryCode == "" {
			found.CountryCode = code
		}
		if code := municipalityCode(e.Tags["ref:ine"]); code != "" && found.MunicipalityCode == "" {
			found.MunicipalityCode = code
		}
	}
	o.remember(key, found)
	return found, nil
}

// ask puts the query to one instance and reads its answer.
func (o *Overpass) ask(ctx context.Context, endpoint, query string, answer *overpassAnswer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"data": {query}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// A generic name: the instance asks who is calling, and nothing more than that is needed.
	req.Header.Set("User-Agent", "intranet-zone-lookup/1.0")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(answer)
}

// validPoint says whether a latitude and a longitude are numbers that fit WGS84.
func validPoint(lat, lon float64) bool {
	ok := func(v, limit float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= limit }
	return ok(lat, 90) && ok(lon, 180)
}

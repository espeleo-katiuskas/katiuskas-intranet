package areas_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
)

// The Nominatim client reads the country and the INE code of the municipality out of what the instance
// answers, keeps the answer for the same point, and calls a failure by its name.
func TestNominatim(t *testing.T) {
	var requests atomic.Int32
	var body string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.Header.Get("User-Agent") == "" || query.Get("zoom") != "10" ||
			query.Get("extratags") != "1" || query.Get("lat") == "" || query.Get("lon") == "" {
			t.Errorf("a GET that says who calls, for a municipality, with its tags: %s %q %v",
				r.Method, r.Header.Get("User-Agent"), query)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	n := areas.NewNominatim(server.URL).Spacing(0)
	ctx := context.Background()

	body = `{"name":"La Cabrera","address":{"country_code":"es"},"extratags":{"ref:ine":"28030000000"}}`
	got, err := n.Containing(ctx, 40.85, -3.62)
	if err != nil || got.MunicipalityCode != "28030" || got.CountryCode != "ES" {
		t.Fatalf("unexpected answer: %+v, %v", got, err)
	}
	// The same point (to about ten metres) is not asked again.
	if _, err := n.Containing(ctx, 40.85001, -3.62001); err != nil || requests.Load() != 1 {
		t.Errorf("expected the answer to be kept, %d requests, %v", requests.Load(), err)
	}

	// Portugal tags its codes `ref:ine` too, with six digits for a parish, and the first five of those are the
	// code of some Spanish municipality (the Azores, near 38.5, -28.5, gave L'Alcúdia de Crespins): they are
	// not taken for one, even if a country were missing from the answer.
	body = `{"name":"Criação Velha","address":{"country_code":"pt"},"extratags":{"ref:ine":"460203"}}`
	if got, err := n.Containing(ctx, 38.5030, -28.5040); err != nil || got != (areas.Containing{CountryCode: "PT"}) {
		t.Errorf("a Portuguese parish is no Spanish municipality: %+v, %v", got, err)
	}
	body = `{"address":{"country_code":"es"},"extratags":{"ref:ine":"460203"}}`
	if got, err := n.Containing(ctx, 39.0, -0.5); err != nil || got.MunicipalityCode != "" {
		t.Errorf("six digits are not a Spanish reference: %+v, %v", got, err)
	}

	// Outside Spain there is no INE code, only the country; a ref that is not a code is not taken for one.
	body = `{"address":{"country_code":"pt"},"extratags":{"ref:ine":"abc"}}`
	if got, err := n.Containing(ctx, 38.7, -9.1); err != nil || got != (areas.Containing{CountryCode: "PT"}) {
		t.Errorf("expected only the country, got %+v, %v", got, err)
	}
	// Nothing there (the sea) is a 200 with an error: an empty answer, not a failure.
	body = `{"error":"Unable to geocode"}`
	if got, err := n.Containing(ctx, 0, -30); err != nil || got != (areas.Containing{}) {
		t.Errorf("expected nothing, got %+v, %v", got, err)
	}

	// Anything but 200, or what is not JSON, is the source being unavailable; and it is not kept.
	status = http.StatusTooManyRequests
	if _, err := n.Containing(ctx, 2, 2); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
	status, body = http.StatusOK, "<html>busy</html>"
	if _, err := n.Containing(ctx, 2, 2); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
	server.Close()
	if _, err := n.Containing(ctx, 3, 3); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable with the server gone, got %v", err)
	}
}

// Requests are spaced out by the second that the usage policy of the public instance asks for, and a
// request that is waiting gives up with its context.
func TestNominatimSpacesRequests(t *testing.T) {
	var times []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		_, _ = w.Write([]byte(`{"address":{"country_code":"es"}}`))
	}))
	defer server.Close()
	n := areas.NewNominatim(server.URL)
	for i := 0; i < 2; i++ {
		if _, err := n.Containing(context.Background(), float64(10+i), 0); err != nil {
			t.Fatal(err)
		}
	}
	if gap := times[1].Sub(times[0]); gap < 900*time.Millisecond {
		t.Errorf("expected about a second between requests, got %v", gap)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := n.Containing(ctx, 20, 0); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected a waiting request to give up with its context, got %v", err)
	}
}

// Searching reads the places out of what the instance answers (its "search" endpoint, next to the
// "reverse" one), skips what is not a point, keeps the answer for the same text and calls a failure by
// its name.
func TestNominatimSearch(t *testing.T) {
	var requests atomic.Int32
	var body string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/search" || r.Header.Get("User-Agent") == "" || r.URL.Query().Get("q") == "" ||
			r.URL.Query().Get("format") != "jsonv2" {
			t.Errorf("a GET of /search that says who calls: %s %q", r.URL.Path, r.URL.RawQuery)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	n := areas.NewNominatim(server.URL + "/reverse").Spacing(0)
	ctx := context.Background()

	body = `[{"display_name":"Valdemanco, Madrid","lat":"40.87","lon":"-3.63","boundingbox":["40.8","40.9","-3.7","-3.6"]},
		{"display_name":"No box","lat":"1","lon":"2"},
		{"display_name":"Broken","lat":"x","lon":"2"},
		{"display_name":"Out","lat":"95","lon":"2"}]`
	got, err := n.Search(ctx, "Valdemanco")
	if err != nil || len(got) != 2 {
		t.Fatalf("two places expected: %+v, %v", got, err)
	}
	if got[0].Name != "Valdemanco, Madrid" || got[0].Lat != 40.87 || got[0].Bounds == nil ||
		got[0].Bounds.South != 40.8 || got[0].Bounds.North != 40.9 || got[0].Bounds.West != -3.7 || got[0].Bounds.East != -3.6 {
		t.Errorf("unexpected first place: %+v", got[0])
	}
	if got[1].Bounds != nil {
		t.Errorf("no box, no bounds: %+v", got[1])
	}
	// The same text, however it is written, is not asked again.
	if _, err := n.Search(ctx, " valdemanco "); err != nil || requests.Load() != 1 {
		t.Errorf("expected the answer to be kept, %d requests, %v", requests.Load(), err)
	}

	status = http.StatusTooManyRequests
	if _, err := n.Search(ctx, "other"); !errors.Is(err, areas.ErrSearchUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
	status, body = http.StatusOK, "<html>busy</html>"
	if _, err := n.Search(ctx, "other"); !errors.Is(err, areas.ErrSearchUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
}

package areas_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
)

// The Overpass client reads the country and the INE code of the municipality out of what the instance
// answers, keeps the answer for the same point, and calls a failure by its name.
func TestOverpass(t *testing.T) {
	var requests atomic.Int32
	var body string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("User-Agent") == "" {
			t.Errorf("a POST that says who calls: %s %q", r.Method, r.Header.Get("User-Agent"))
		}
		_ = r.ParseForm()
		if q := r.PostForm.Get("data"); q == "" {
			t.Errorf("no query sent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	o := areas.NewOverpass(server.URL)
	ctx := context.Background()

	body = `{"elements":[
		{"tags":{"admin_level":"8","name":"La Cabrera","ref:ine":"28030000000"}},
		{"tags":{"admin_level":"2","ISO3166-1":"es"}}]}`
	got, err := o.Containing(ctx, 40.85, -3.62)
	if err != nil || got.MunicipalityCode != "28030" || got.CountryCode != "ES" {
		t.Fatalf("unexpected answer: %+v, %v", got, err)
	}
	// The same point (to about ten metres) is not asked again.
	if _, err := o.Containing(ctx, 40.85001, -3.62001); err != nil || requests.Load() != 1 {
		t.Errorf("expected the answer to be kept, %d requests, %v", requests.Load(), err)
	}

	// A ref that is not an INE code is not taken for one; no boundaries is an empty answer.
	body = `{"elements":[{"tags":{"admin_level":"8","ref:ine":"abc"}}]}`
	if got, err := o.Containing(ctx, 1, 1); err != nil || got != (areas.Containing{}) {
		t.Errorf("expected nothing, got %+v, %v", got, err)
	}

	// Anything but 200, or what is not JSON, is the source being unavailable; and it is not kept.
	status = http.StatusTooManyRequests
	if _, err := o.Containing(ctx, 2, 2); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
	status, body = http.StatusOK, "<html>busy</html>"
	if _, err := o.Containing(ctx, 2, 2); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable, got %v", err)
	}
	server.Close()
	if _, err := o.Containing(ctx, 3, 3); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable with the server gone, got %v", err)
	}
}

// An instance that is busy is skipped for the next one.
func TestOverpassFallsBack(t *testing.T) {
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer busy.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"elements":[{"tags":{"ISO3166-1":"FR"}}]}`))
	}))
	defer good.Close()
	got, err := areas.NewOverpass(busy.URL, good.URL).Containing(context.Background(), 48, 2)
	if err != nil || got.CountryCode != "FR" {
		t.Fatalf("expected the second instance to answer, got %+v, %v", got, err)
	}
	if _, err := areas.NewOverpass(busy.URL, busy.URL).Containing(context.Background(), 48, 2); !errors.Is(err, areas.ErrLocateUnavailable) {
		t.Errorf("expected unavailable when all are busy, got %v", err)
	}
}

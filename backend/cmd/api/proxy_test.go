package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
)

// With CATALOGUE_URL set the program is a client of the catalogue: the places are the catalogue's (forwarded
// as they were asked, with the count of activities added), an activity that goes to a place is told to it as
// a reference, a place it does not know is refused, and none of the catalogue's own routes are served here.
func TestCatalogueAsAClient(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		sig, _ := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
		if len(parts) != 3 || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/places/refs":
			// Place 77 exists; nothing else does.
			if r.URL.Query().Get("ids") == "77" {
				_, _ = io.WriteString(w, `[{"id":77,"name":"Cueva remota","type_name":"Cueva","where":"Madrid, España"}]`)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		case strings.Contains(r.URL.Path, "/references/"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/places":
			_, _ = io.WriteString(w, `[{"id":77,"type_id":1,"kind":"cave","area_ids":[1],"name":"Cueva remota"}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()

	tx := testutil.NewTx(t)
	cfg := testConfig(t)
	cfg.CatalogueURL = fake.URL
	cfg.CatalogueIssuer = "katiuskas-intranet"
	cfg.CataloguePrivateKey = base64.StdEncoding.EncodeToString(priv)
	router, _, _, _, err := buildRouter(cfg, tx, alwaysHealthyPinger{}, telegram.NewSenderFromConfig("", ""), nil)
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	member := testutil.InsertMember(t, tx)
	token, err := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx))).Issue(t.Context(), member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(http.MethodGet, "/api/v1/places", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"activity_count":0`) || !strings.Contains(rec.Body.String(), "Cueva remota") {
		t.Fatalf("GET /places through the proxy: %d %s", rec.Code, rec.Body.String())
	}

	activity := `{"title":"Salida","type":"caving","start_date":"2030-01-05","end_date":"2030-01-05","place_ids":[%s]}`
	if rec := call(http.MethodPost, "/api/v1/activities", strings.Replace(activity, "%s", "5", 1)); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "unknown_place") {
		t.Fatalf("a place the catalogue does not know: %d %s", rec.Code, rec.Body.String())
	}
	rec := call(http.MethodPost, "/api/v1/activities", strings.Replace(activity, "%s", "77", 1))
	if rec.Code != http.StatusCreated {
		t.Fatalf("an activity at a place of the catalogue: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// The count is the club's: the activity just made is the one that went to place 77.
	if rec := call(http.MethodGet, "/api/v1/places", ""); !strings.Contains(rec.Body.String(), `"activity_count":1`) {
		t.Fatalf("the count of activities: %s", rec.Body.String())
	}
	mu.Lock()
	joined := strings.Join(seen, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "PUT /api/v1/places/77/references/activity:") {
		t.Fatalf("the catalogue was not told that the club uses the place:\n%s", joined)
	}

	// Nothing of the catalogue is served from here but the forwarding: the public site is the catalogue's.
	if rec := call(http.MethodGet, "/api/v1/public/places", ""); rec.Code != http.StatusNotFound {
		t.Errorf("public routes in proxy mode: %d, want 404", rec.Code)
	}
}

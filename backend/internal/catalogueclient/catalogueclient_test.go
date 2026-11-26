package catalogueclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
)

// goldenSeed, goldenClaims and goldenToken are the vector that the catalogue (internal/assertion) must
// make as well: Ed25519 is deterministic, so two implementations that agree on the format produce the same
// bytes. If this fails the intranet and the catalogue no longer understand each other.
func goldenSeed() []byte {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	return seed
}

const goldenToken = "v1.eyJpc3MiOiJrYXRpdXNrYXMtaW50cmFuZXQiLCJzdWIiOiIwYjlkNmMxZS0xMTExLTQyMjItODMzMy00NDQ0NTU1NTY2NjYiLCJuYW1lIjoiYW5hIiwiZ3JvdXBzIjpbImthdGl1c2thcyJdLCJyb2xlcyI6WyJib2FyZCJdLCJpYXQiOjE3MDAwMDAwMDAsImV4cCI6MTcwMDAwMDA2MH0.6GSBn7Fv42bp8kHYypqOMbPofvV3W8kBTWvrUJrSlz0RbbBMfcyZY22bePT8RzFNmnD6cvDsmRCS9QiIo9-pBA"

func TestAssertionMatchesTheCataloguesVector(t *testing.T) {
	got, err := sign(ed25519.NewKeyFromSeed(goldenSeed()), claims{
		Issuer: "katiuskas-intranet", Subject: "0b9d6c1e-1111-4222-8333-444455556666", Name: "ana",
		Groups: []string{"katiuskas"}, Roles: []string{"board"}, IssuedAt: 1700000000, ExpiresAt: 1700000060,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != goldenToken {
		t.Fatalf("assertion = %s, want %s", got, goldenToken)
	}
}

// fakeCatalogue checks every assertion as the real one does (signature, issuer, lifetime) and keeps what
// it was told.
type fakeCatalogue struct {
	t   *testing.T
	pub ed25519.PublicKey
	srv *httptest.Server

	mu       sync.Mutex
	requests []string
	subjects []string
	roles    [][]string
	cookies  []string
	refs     map[string]bool
}

func newFakeCatalogue(t *testing.T, pub ed25519.PublicKey, handler http.HandlerFunc) *fakeCatalogue {
	f := &fakeCatalogue{t: t, pub: pub, refs: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, ok := verify(token, f.pub)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"unauthorized","error":"no"}`)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		f.subjects = append(f.subjects, c.Subject)
		f.roles = append(f.roles, c.Roles)
		f.cookies = append(f.cookies, r.Header.Get("Cookie"))
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func verify(token string, pub ed25519.PublicKey) (claims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return claims{}, false
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return claims{}, false
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return claims{}, false
	}
	var c claims
	if json.Unmarshal(payload, &c) != nil || c.ExpiresAt-c.IssuedAt > int64(maxLifetime/time.Second) ||
		time.Now().Unix() > c.ExpiresAt {
		return claims{}, false
	}
	return c, true
}

func keys(t *testing.T) (ed25519.PublicKey, string) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, base64.StdEncoding.EncodeToString(priv)
}

type fakeResolver map[string]auth.AuthenticatedUser

func (f fakeResolver) Authenticate(_ context.Context, token, _, _ string) (auth.AuthenticatedUser, error) {
	if u, ok := f[token]; ok {
		return u, nil
	}
	return auth.AuthenticatedUser{}, auth.ErrInvalidToken
}

var ana = auth.AuthenticatedUser{ID: 1, Subject: "sub-ana", Login: "ana", Roles: []string{"board", "material"}}

// ctxOf is the context of a request of the person, as the authenticator makes it.
func ctxOf(u auth.AuthenticatedUser) context.Context {
	var ctx context.Context
	a := auth.NewAuthenticator(fakeResolver{"t": u})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer t")
	a.RequireAuth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return ctx
}

func TestPlaceRefsAsksTheCatalogueAsThePerson(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":3,"name":"Sima","type_name":"Sima","where":"Madrid, España"}]`)
	})
	c, err := New(fake.srv.URL, "katiuskas-intranet", priv)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := c.PlaceRefs(ctxOf(ana), []int64{3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != 3 || refs[0].Where != "Madrid, España" {
		t.Fatalf("refs = %+v", refs)
	}
	if fake.requests[0] != "GET /api/v1/places/refs?ids=3%2C4" {
		t.Fatalf("request = %q", fake.requests[0])
	}
	if fake.subjects[0] != "sub-ana" {
		t.Fatalf("subject = %q", fake.subjects[0])
	}
	// Only admin and board go as roles: the catalogue asks for nothing else.
	if len(fake.roles[0]) != 1 || fake.roles[0][0] != "board" {
		t.Fatalf("roles = %v", fake.roles[0])
	}
}

func TestPlaceRefsSplitsManyIDs(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	c, _ := New(fake.srv.URL, "katiuskas-intranet", priv)
	ids := make([]int64, 450)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err := c.PlaceRefs(ctxOf(ana), ids); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 3 {
		t.Fatalf("%d requests, want 3", len(fake.requests))
	}
}

func TestCallsNeedSomebodyToVouchFor(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	c, _ := New(fake.srv.URL, "katiuskas-intranet", priv)
	if _, err := c.PlaceRefs(context.Background(), []int64{1}); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("err = %v, want ErrNoPrincipal", err)
	}
	noSubject := auth.AuthenticatedUser{ID: 2, Login: "x", Roles: []string{"member"}}
	if _, err := c.PlaceRefs(ctxOf(noSubject), []int64{1}); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("a session with no subject: err = %v", err)
	}
	ctx := WithPrincipal(context.Background(), SystemPrincipal("katiuskas"))
	if err := c.Refer(ctx, 1, "activity:1"); err != nil {
		t.Fatalf("system principal: %v", err)
	}
}

func TestReferAndUnrefer(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/404") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/places/9/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"not_found","error":"no"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := New(fake.srv.URL, "katiuskas-intranet", priv)
	ctx := ctxOf(ana)
	if err := c.Refer(ctx, 5, "activity:7"); err != nil {
		t.Fatal(err)
	}
	if err := c.Unrefer(ctx, 5, "activity:7"); err != nil {
		t.Fatal(err)
	}
	want := []string{"PUT /api/v1/places/5/references/activity:7", "DELETE /api/v1/places/5/references/activity:7"}
	for i, w := range want {
		if fake.requests[i] != w {
			t.Fatalf("request %d = %q, want %q", i, fake.requests[i], w)
		}
	}
	var refused *ErrRefused
	if err := c.Refer(ctx, 9, "activity:7"); !errors.As(err, &refused) || refused.Status != 404 || refused.Code != "not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRefusesWhatIsNotAConfiguration(t *testing.T) {
	_, priv := keys(t)
	for name, args := range map[string][3]string{
		"address": {"cuevasysimas.es", "katiuskas-intranet", priv},
		"issuer":  {"https://x.test", " ", priv},
		"key":     {"https://x.test", "katiuskas-intranet", "not base64!"},
		"size":    {"https://x.test", "katiuskas-intranet", base64.StdEncoding.EncodeToString([]byte("short"))},
	} {
		if _, err := New(args[0], args[1], args[2]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The seed alone is accepted.
	if _, err := New("https://x.test", "i", base64.StdEncoding.EncodeToString(goldenSeed())); err != nil {
		t.Errorf("seed: %v", err)
	}
}

type fixedCounts map[int64]int

func (f fixedCounts) ActivityCounts(_ context.Context, ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	for _, id := range ids {
		if n, ok := f[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

func proxyServer(t *testing.T, fake *fakeCatalogue, priv string, counts ActivityCounter) *httptest.Server {
	c, err := New(fake.srv.URL, "katiuskas-intranet", priv)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewProxy(c, auth.NewAuthenticator(fakeResolver{"tok-ana": ana, "tok-nobody": {ID: 9, Subject: "s", Login: "n", Roles: []string{"material"}}}), counts).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path, token string, cookie string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

const listing = `[{"id":1,"type_id":1,"kind":"cave","area_ids":[1],"name":"A","withheld":[]},` +
	`{"id":2,"type_id":1,"kind":"cave","area_ids":[1],"name":"B","withheld":["activities"]},` +
	`{"id":3,"type_id":1,"kind":"cave","area_ids":[1],"name":"C"}]`

func TestProxyForwardsAsTheAssertionAndAddsTheCounts(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, listing)
	})
	srv := proxyServer(t, fake, priv, fixedCounts{1: 4, 2: 9})

	resp, body := get(t, srv, "/api/v1/places?q=x", "tok-ana", "katiuskas_session=secret")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	counts := []float64{got[0]["activity_count"].(float64), got[1]["activity_count"].(float64), got[2]["activity_count"].(float64)}
	// B withholds the activities: zero, whatever the club counts. C has none.
	if counts[0] != 4 || counts[1] != 0 || counts[2] != 0 {
		t.Fatalf("counts = %v", counts)
	}
	if fake.requests[0] != "GET /api/v1/places?q=x" || fake.subjects[0] != "sub-ana" {
		t.Fatalf("catalogue saw %v as %v", fake.requests, fake.subjects)
	}
	if fake.cookies[0] != "" {
		t.Fatalf("the session cookie went to the catalogue: %q", fake.cookies[0])
	}
}

func TestProxyRefusesWhoIsNotAMemberAndWhoIsNotSignedIn(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	srv := proxyServer(t, fake, priv, nil)
	if resp, _ := get(t, srv, "/api/v1/places", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv, "/api/v1/places", "tok-nobody", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a person with only material: %d", resp.StatusCode)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("the catalogue was asked: %v", fake.requests)
	}
}

func TestProxyPassesTheCataloguesErrorsThrough(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"place_in_use","error":"in use"}`)
	})
	srv := proxyServer(t, fake, priv, fixedCounts{})
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/places/1", nil)
	req.Header.Set("Authorization", "Bearer tok-ana")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "place_in_use") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestProxyAcceptsTheCookieOnlyForFiles(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png")
	})
	srv := proxyServer(t, fake, priv, fixedCounts{})
	if resp, _ := get(t, srv, "/api/v1/places/1/files/2/file", "", "katiuskas_session=tok-ana"); resp.StatusCode != http.StatusOK {
		t.Fatalf("file with a cookie: %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv, "/api/v1/places/1", "", "katiuskas_session=tok-ana"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a place with only a cookie: %d", resp.StatusCode)
	}
}

func TestProxyWhenTheCatalogueIsDown(t *testing.T) {
	pub, priv := keys(t)
	fake := newFakeCatalogue(t, pub, func(http.ResponseWriter, *http.Request) {})
	srv := proxyServer(t, fake, priv, nil)
	fake.srv.Close()
	resp, body := get(t, srv, "/api/v1/places", "tok-ana", "")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "catalogue_unavailable") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestIsFileRoute(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/places/1/files/2/file":  true,
		"/api/v1/systems/1/files/2/file": true,
		"/api/v1/places/1/files/2":       false,
		"/api/v1/areas/1/files/2/file":   false,
		"/api/v1/places":                 false,
	} {
		if isFileRoute(path) != want {
			t.Errorf("isFileRoute(%q) = %v", path, !want)
		}
	}
}

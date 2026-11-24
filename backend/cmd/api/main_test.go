package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/adminstatus"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/config"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

type alwaysHealthyPinger struct{}

func (alwaysHealthyPinger) Ping(context.Context) error { return nil }

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		HTTPAddr:              ":0",
		TokenTTL:              time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     time.Minute,
		TelegramBotDMLinkTTL:  5 * time.Minute,
		ImpersonationTokenTTL: time.Hour,
		PublicBaseURL:         "http://localhost:64710",
		// Real directories are required (archive.NewService et al. stat
		// them), but nothing here should ever touch a host path — a
		// fresh t.TempDir() per root, cleaned up automatically.
		ArchiveRoot:           t.TempDir(),
		UserFilesRoot:         t.TempDir(),
		ActivityResourcesRoot: t.TempDir(),
		TransactionFilesRoot:  t.TempDir(),
		LibraryFilesRoot:      t.TempDir(),
		PlaceFilesRoot:        t.TempDir(),
	}
}

// TestBuildRouterAssemblesAndAnswers is a smoke test of the whole
// composition root main() wires up. Every domain package already has
// its own handler tests covering its specific behavior in isolation;
// this is the one thing none of them can catch — that buildRouter
// itself (main()'s own wiring, previously untested at all) assembles
// without error against a real, migrated database and that the
// resulting router answers the way an operator or the frontend would
// actually observe, end to end through the real mux rather than one
// package's handler in isolation.
func TestBuildRouterAssemblesAndAnswers(t *testing.T) {
	tx := testutil.NewTx(t)
	cfg := testConfig(t)
	telegramSender := telegram.NewSenderFromConfig("", "")

	router, authHandler, usersService, _, err := buildRouter(cfg, tx, alwaysHealthyPinger{}, telegramSender, nil)
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	if authHandler == nil || usersService == nil {
		t.Fatal("expected buildRouter to also return a non-nil authHandler and usersService")
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/api/v1/health"); rec.Code != http.StatusOK {
		t.Errorf("GET /health: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := get("/api/v1/auth/me"); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /auth/me with no token: expected 401, got %d", rec.Code)
	}
	if rec := get("/api/v1/does-not-exist"); rec.Code != http.StatusNotFound {
		t.Errorf("GET an unknown route: expected 404, got %d", rec.Code)
	}
}

// TestCatalogueIsWiredToTheUsers checks, through the real router, the two things the catalogue takes
// from the users: it names the author of a place (a service made before the users would hold a nil
// lookup and panic here), and a person who authored something cannot be deleted (there is no foreign
// key to say it, so the users must have been told to ask the catalogue).
func TestCatalogueIsWiredToTheUsers(t *testing.T) {
	tx := testutil.NewTx(t)
	router, _, usersService, _, err := buildRouter(testConfig(t), tx, alwaysHealthyPinger{}, telegram.NewSenderFromConfig("", ""), nil)
	if err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	ctx := context.Background()
	member := testutil.InsertMember(t, tx)
	token, err := tokens.NewService(tokens.NewRepository(tx), roles.NewService(roles.NewRepository(tx))).Issue(ctx, member.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue a token: %v", err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	var typeID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.place_types WHERE name = 'Cavidad'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	var subtypeID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.place_subtypes ORDER BY id LIMIT 1`).Scan(&subtypeID); err != nil {
		t.Fatal(err)
	}
	var areaID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.areas WHERE parent_id IS NULL ORDER BY id LIMIT 1`).Scan(&areaID); err != nil {
		t.Fatal(err)
	}
	created := call(http.MethodPost, "/api/v1/places", fmt.Sprintf(
		`{"type_id":%d,"subtype_id":%d,"area_id":%d,"name":"Cueva del cableado","lat":40.4,"lon":-3.7}`, typeID, subtypeID, areaID))
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /places: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var place struct {
		ID        int64  `json:"id"`
		CreatedBy string `json:"created_by"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &place); err != nil {
		t.Fatal(err)
	}
	if place.CreatedBy != member.Login {
		t.Errorf("created_by: got %q, want the login %q", place.CreatedBy, member.Login)
	}
	if rec := call(http.MethodGet, fmt.Sprintf("/api/v1/places/%d", place.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("GET the place: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The catalogue and the activities are made one with the other in hand: a listing, which asks the
	// activities how many went to each place, and the page of a user's places, which asks which.
	if rec := call(http.MethodGet, "/api/v1/places", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /places: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodGet, "/api/v1/users/"+member.Login+"/places", ""); rec.Code != http.StatusOK {
		t.Errorf("GET the places a person visited: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodGet, fmt.Sprintf("/api/v1/places/%d/activities", place.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("GET the activities of a place: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := usersService.Delete(ctx, member.ID); !errors.Is(err, users.ErrHasReferences) {
		t.Errorf("deleting the author of a place: expected ErrHasReferences, got %v", err)
	}
	// The same for somebody who only added a zone.
	zoner := testutil.InsertMember(t, tx)
	if _, err := tx.Exec(ctx, `INSERT INTO catalogue.areas (parent_id, kind, name, norm_name, created_by) VALUES ($1, 'Región', 'Zona de prueba', 'zona de prueba', $2)`, areaID, zoner.ID); err != nil {
		t.Fatal(err)
	}
	if err := usersService.Delete(ctx, zoner.ID); !errors.Is(err, users.ErrHasReferences) {
		t.Errorf("deleting the author of a zone: expected ErrHasReferences, got %v", err)
	}
}

func TestStartupMessageSaysWhatIsKnownAboutTheBuild(t *testing.T) {
	// The message is in the server's time zone: the test gives one, so it gives the same anywhere (and
	// does not change time.Local, which other goroutines read).
	zone := time.FixedZone("test", 2*60*60)
	commit := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC) // 12:00 there
	cases := []struct {
		name  string
		build adminstatus.Build
		want  string
	}{
		{"both", adminstatus.Build{Version: "v0.3.6", CommitTime: commit}, "🚀 API started (version v0.3.6, commit del 17-10-2026 12:00)"},
		{"version only", adminstatus.Build{Version: "v0.3.6"}, "🚀 API started (version v0.3.6)"},
		{"date only", adminstatus.Build{CommitTime: commit}, "🚀 API started (commit del 17-10-2026 12:00)"},
		{"nothing known", adminstatus.Build{}, "🚀 API started"},
	}
	for _, c := range cases {
		if got := startupMessageIn(c.build, zone); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// On a signal the server stops taking requests but lets the one being answered finish, and only
// then does what is left to do before exiting.
func TestServeFinishesTheRequestsInFlightBeforeTheLastWrites(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	started := make(chan struct{})
	var order []string
	var mu sync.Mutex
	note := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, what)
	}
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		note("request done")
		w.WriteHeader(http.StatusOK)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, server, 5*time.Second, func(context.Context) { note("finish") })
	}()

	var response *http.Response
	requested := make(chan error, 1)
	go func() {
		var err error
		for range 50 { // the server may not be listening yet
			response, err = http.Get("http://" + addr + "/")
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		requested <- err
	}()
	<-started
	cancel()

	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if err := <-requested; err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("the request in flight was not answered: %v %v", err, response)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "request done" || order[1] != "finish" {
		t.Errorf("expected the request to finish before the last writes, got %v", order)
	}
}

// A server that cannot start is an error, not a shutdown.
func TestServeReportsAServerThatFails(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	server := &http.Server{Addr: taken.Addr().String(), Handler: http.NotFoundHandler()}
	if err := serve(context.Background(), server, time.Second, func(context.Context) {}); err == nil {
		t.Error("expected an error for an address already in use")
	}
}

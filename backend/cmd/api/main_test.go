package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/adminstatus"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/config"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
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
		// Real directories are required (library.NewService et al. stat
		// them), but nothing here should ever touch a host path — a
		// fresh t.TempDir() per root, cleaned up automatically.
		LibraryRoot:           t.TempDir(),
		UserFilesRoot:         t.TempDir(),
		ActivityResourcesRoot: t.TempDir(),
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

	router, authHandler, usersService, err := buildRouter(cfg, tx, alwaysHealthyPinger{}, telegramSender, nil)
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

func TestStartupMessageSaysWhatIsKnownAboutTheBuild(t *testing.T) {
	// Mid-morning UTC, so the date is the same wherever the test runs.
	commit := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		build adminstatus.Build
		want  string
	}{
		{"both", adminstatus.Build{Version: "v0.3.6", CommitTime: commit}, "🚀 API started (version v0.3.6, commit del 17-10-2026)"},
		{"version only", adminstatus.Build{Version: "v0.3.6"}, "🚀 API started (version v0.3.6)"},
		{"date only", adminstatus.Build{CommitTime: commit}, "🚀 API started (commit del 17-10-2026)"},
		{"nothing known", adminstatus.Build{}, "🚀 API started"},
	}
	for _, c := range cases {
		if got := startupMessage(c.build); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

package adminstatus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/adminstatus"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func newServer(t *testing.T, info adminstatus.Info) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	t.Helper()
	tx := testutil.NewTx(t)
	authenticator, resolver := testutil.NewTestAuthenticator()
	mux := http.NewServeMux()
	adminstatus.NewHandler(tx, authenticator, info).RegisterRoutes(mux)
	return mux, resolver, tx
}

func get(mux *http.ServeMux, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestStatusIsAdminOnly(t *testing.T) {
	mux, resolver, _ := newServer(t, adminstatus.Info{})
	for _, role := range []string{"board", "member"} {
		if rec := get(mux, testutil.TokenFor(resolver, 1, role)); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden, got %d", role, rec.Code)
		}
	}
	if rec := get(mux, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without a token, got %d", rec.Code)
	}
}

func TestStatusReportsWhatTheProcessWasGiven(t *testing.T) {
	existing := t.TempDir()
	started := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	mux, resolver, tx := newServer(t, adminstatus.Info{
		StartedAt:             started,
		PublicBaseURL:         "https://intranet.example.test",
		SessionTTL:            30 * 24 * time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     time.Minute,
		ImpersonationTTL:      time.Hour,
		TelegramBotDMLinkTTL:  5 * time.Minute,
		SMTPConfigured:        true,
		TelegramLogConfigured: true,
		TelegramBotRunning:    true,
		TelegramBotUsername:   "KatiuskasBot",
		LibraryRoot:           existing,
		UserFilesRoot:         existing + "/missing",
		ActivityResourcesRoot: existing,
	})
	// The test database is migrated without goose; give it the table goose
	// keeps, so the migration number can be read back.
	if _, err := tx.Exec(context.Background(), `
		CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL, tstamp timestamp DEFAULT now());
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true), (19, true), (20, true), (21, false)
	`); err != nil {
		t.Fatalf("create goose table: %v", err)
	}

	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		App struct {
			Version   string    `json:"version"`
			GoVersion string    `json:"go_version"`
			StartedAt time.Time `json:"started_at"`
		} `json:"app"`
		Database struct {
			OK               bool   `json:"ok"`
			MigrationVersion *int64 `json:"migration_version"`
		} `json:"database"`
		Mail     map[string]bool `json:"mail"`
		Telegram struct {
			LogChannelConfigured bool   `json:"log_channel_configured"`
			BotRunning           bool   `json:"bot_running"`
			BotUsername          string `json:"bot_username"`
		} `json:"telegram"`
		Settings map[string]any `json:"settings"`
		Storage  []struct {
			Name   string `json:"name"`
			Exists bool   `json:"exists"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.App.GoVersion == "" || !got.App.StartedAt.Equal(started) {
		t.Errorf("unexpected app section: %+v", got.App)
	}
	if !got.Database.OK || got.Database.MigrationVersion == nil || *got.Database.MigrationVersion != 20 {
		t.Errorf("expected a working database at migration 20 (the latest *applied* one), got %+v", got.Database)
	}
	if !got.Mail["smtp_configured"] || !got.Telegram.LogChannelConfigured || !got.Telegram.BotRunning || got.Telegram.BotUsername != "KatiuskasBot" {
		t.Errorf("unexpected mail/telegram: %v / %+v", got.Mail, got.Telegram)
	}
	if got.Settings["session_ttl_seconds"] != float64(30*24*3600) || got.Settings["magic_link_ttl_seconds"] != float64(900) || got.Settings["public_base_url"] != "https://intranet.example.test" {
		t.Errorf("unexpected settings: %v", got.Settings)
	}
	exists := map[string]bool{}
	for _, s := range got.Storage {
		exists[s.Name] = s.Exists
	}
	if !exists["library"] || exists["user_files"] || !exists["activity_resources"] {
		t.Errorf("expected only the missing directory to be reported as missing, got %v", exists)
	}
	for _, secret := range []string{"password", "postgres://", "smtp_host", "bot_token"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(secret)) {
			t.Errorf("expected nothing secret-looking in the response, found %q in %s", secret, rec.Body.String())
		}
	}
}

// A database with no goose table (as in the tests, or hand-built) is still
// reported as working; it just has no migration number to show.
func TestStatusWithoutAGooseTable(t *testing.T) {
	mux, resolver, _ := newServer(t, adminstatus.Info{})
	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	var got struct {
		Database map[string]any `json:"database"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("expected 200 and JSON, got %d: %s", rec.Code, rec.Body.String())
	}
	if got.Database["ok"] != true {
		t.Errorf("expected the database to be ok, got %v", got.Database)
	}
	if _, present := got.Database["migration_version"]; present {
		t.Errorf("expected no migration_version without a goose table, got %v", got.Database)
	}
}

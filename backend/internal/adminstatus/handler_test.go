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
		LocalSessionTTL:       3 * time.Hour,
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
		ActivityResourcesRoot: existing, TransactionFilesRoot: existing,
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
	if got.Settings["local_session_ttl_seconds"] != float64(3*3600) {
		t.Errorf("unexpected session lifetime for local addresses: %v", got.Settings["local_session_ttl_seconds"])
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

func TestStatusListsProblemsAndDisks(t *testing.T) {
	existing := t.TempDir()
	mux, resolver, _ := newServer(t, adminstatus.Info{
		LibraryRoot:           existing,
		UserFilesRoot:         existing + "/missing",
		ActivityResourcesRoot: existing, TransactionFilesRoot: existing,
		DiskAlertPercent: 1, // any real filesystem is at least that full
	})
	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	var got struct {
		Problems []string `json:"problems"`
		Disks    []struct {
			Paths       []string `json:"paths"`
			UsedPercent int      `json:"used_percent"`
			TotalBytes  uint64   `json:"total_bytes"`
		} `json:"disks"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("expected 200 and JSON, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(got.Disks) != 1 || len(got.Disks[0].Paths) != 3 || got.Disks[0].TotalBytes == 0 {
		t.Errorf("expected one filesystem with the three directories that exist, got %+v", got.Disks)
	}
	if len(got.Problems) != 2 {
		t.Errorf("expected the missing directory and the full disk as problems, got %q", got.Problems)
	}
}

func TestStatusHasNoProblemsWhenAllIsWell(t *testing.T) {
	existing := t.TempDir()
	mux, resolver, _ := newServer(t, adminstatus.Info{
		LibraryRoot: existing, UserFilesRoot: existing, ActivityResourcesRoot: existing, TransactionFilesRoot: existing,
	})
	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	if !strings.Contains(rec.Body.String(), `"problems":[]`) {
		t.Errorf("expected an empty list of problems (not null), got %s", rec.Body.String())
	}
}

// With no storage directory at all there is nothing to measure: the lists are empty, never null.
func TestStatusListsAreNeverNull(t *testing.T) {
	mux, resolver, _ := newServer(t, adminstatus.Info{})
	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, field := range []string{`"disks":[]`, `"problems":[`} {
		if !strings.Contains(rec.Body.String(), field) {
			t.Errorf("expected %s in %s", field, rec.Body.String())
		}
	}
}

// The activity figures: the people who used a session in each period, once each and leaving out an
// administrator acting as somebody else, and the requests the API has counted.
func TestStatusReportsTheActivity(t *testing.T) {
	mux, resolver, tx := newServer(t, adminstatus.Info{})
	ctx := context.Background()
	// last_used_at of each session, relative to now.
	session := func(user testutil.TestUser, lastUsed string, impersonatedBy *int64) {
		t.Helper()
		if _, err := tx.Exec(ctx, `
			INSERT INTO tokens (user_id, token_hash, expires_at, last_used_at, impersonated_by)
			VALUES ($1, md5(random()::text), now() + interval '1 day', now() - $2::interval, $3)
		`, user.ID, lastUsed, impersonatedBy); err != nil {
			t.Fatal(err)
		}
	}
	now := testutil.InsertMember(t, tx)   // used 10 minutes ago, on two sessions
	today := testutil.InsertMember(t, tx) // 5 hours ago
	month := testutil.InsertMember(t, tx) // 10 days ago
	old := testutil.InsertMember(t, tx)   // 2 months ago
	never := testutil.InsertMember(t, tx) // a session never used
	admin := testutil.InsertMember(t, tx) // acting as `now`: not their activity
	session(now, "10 minutes", nil)
	session(now, "20 minutes", nil)
	session(today, "5 hours", nil)
	session(month, "10 days", nil)
	session(old, "2 months", nil)
	if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, 'never-used', now() + interval '1 day')`, never.ID); err != nil {
		t.Fatal(err)
	}
	session(admin, "1 minute", &now.ID)

	for _, c := range []struct {
		ago      string
		requests int
	}{{"5 minutes", 100}, {"50 minutes", 20}, {"3 hours", 7}, {"5 days", 3}, {"2 months", 1000}} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO request_counts (minute, requests) VALUES (date_trunc('minute', now() - $1::interval), $2)
		`, c.ago, c.requests); err != nil {
			t.Fatal(err)
		}
	}

	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Activity struct {
			Users    map[string]int64 `json:"users"`
			Requests map[string]int64 `json:"requests"`
		} `json:"activity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Whoever the other tests of this database left behind is not here: it is a transaction that is
	// rolled back.
	wantUsers := map[string]int64{"last_hour": 1, "last_day": 2, "last_month": 3}
	wantRequests := map[string]int64{"last_hour": 120, "last_day": 127, "last_month": 130}
	for key, want := range wantUsers {
		if got := body.Activity.Users[key]; got != want {
			t.Errorf("users %s: expected %d, got %d", key, want, got)
		}
	}
	for key, want := range wantRequests {
		if got := body.Activity.Requests[key]; got != want {
			t.Errorf("requests %s: expected %d, got %d", key, want, got)
		}
	}
}

// The activity figures are read after what is still in memory has been stored.
type flushFunc func(ctx context.Context) error

func (f flushFunc) Flush(ctx context.Context) error { return f(ctx) }

func TestStatusStoresWhatIsInMemoryBeforeReadingTheActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	authenticator, resolver := testutil.NewTestAuthenticator()
	mux := http.NewServeMux()
	handler := adminstatus.NewHandler(tx, authenticator, adminstatus.Info{})
	flushed := 0
	handler.SetFlusher(flushFunc(func(ctx context.Context) error {
		flushed++
		_, err := tx.Exec(ctx, `INSERT INTO request_counts (minute, requests) VALUES (date_trunc('minute', now()), 42)`)
		return err
	}))
	handler.RegisterRoutes(mux)

	rec := get(mux, testutil.TokenFor(resolver, 1, "admin"))
	var body struct {
		Activity struct {
			Requests map[string]int64 `json:"requests"`
		} `json:"activity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if flushed != 1 || body.Activity.Requests["last_hour"] != 42 {
		t.Errorf("flushed %d times, requests of the last hour: %d (expected 1 and 42)", flushed, body.Activity.Requests["last_hour"])
	}
}

func getActivity(mux *http.ServeMux, token, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The calendar of activity: who used the API each day (by login, with their requests), only the days
// with some, oldest first, inside the range asked for; admin only.
func TestActivityCalendarListsWhoUsedTheAPIEachDay(t *testing.T) {
	mux, resolver, tx := newServer(t, adminstatus.Info{})
	ctx := context.Background()
	ana, beto := testutil.InsertMember(t, tx), testutil.InsertMember(t, tx)
	// Days counted back from today in the club's time zone, as the handler does.
	for _, row := range []struct {
		user    testutil.TestUser
		daysAgo int
		count   int
	}{{ana, 0, 5}, {beto, 0, 2}, {ana, 3, 7}, {beto, 400, 9}} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_activity (day, user_id, requests)
			VALUES ((now() AT TIME ZONE 'Europe/Madrid')::date - $3::int, $1, $2)
		`, row.user.ID, row.count, row.daysAgo); err != nil {
			t.Fatal(err)
		}
	}
	token := testutil.TokenFor(resolver, 1, "admin")

	for _, role := range []string{"board", "member"} {
		if rec := getActivity(mux, testutil.TokenFor(resolver, 1, role), ""); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden, got %d", role, rec.Code)
		}
	}
	rec := getActivity(mux, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
		Days []struct {
			Date  string `json:"date"`
			Users []struct {
				Login    string `json:"login"`
				Requests int64  `json:"requests"`
			} `json:"users"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Two days with activity inside the default year; the one 400 days ago is out of it.
	if len(body.Days) != 2 || body.Days[0].Date >= body.Days[1].Date || body.Days[1].Date != body.To {
		t.Fatalf("unexpected days: %+v (to %s)", body.Days, body.To)
	}
	if len(body.Days[0].Users) != 1 || body.Days[0].Users[0].Login != ana.Login || body.Days[0].Users[0].Requests != 7 {
		t.Errorf("unexpected first day: %+v", body.Days[0])
	}
	if len(body.Days[1].Users) != 2 || body.Days[1].Users[0].Requests+body.Days[1].Users[1].Requests != 7 {
		t.Errorf("unexpected last day: %+v", body.Days[1])
	}
	// A wider range reaches it; a narrower one leaves the older day out; nonsense is refused.
	if rec := getActivity(mux, token, "?days=730"); !strings.Contains(rec.Body.String(), `"requests":9`) {
		t.Errorf("730 days should reach the one of 400 days ago: %s", rec.Body.String())
	}
	if rec := getActivity(mux, token, "?days=1"); strings.Count(rec.Body.String(), `"date"`) != 1 {
		t.Errorf("one day should have only today: %s", rec.Body.String())
	}
	for _, bad := range []string{"?days=0", "?days=731", "?days=abc"} {
		if rec := getActivity(mux, token, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", bad, rec.Code)
		}
	}
}

// Package adminstatus answers GET /admin/status: a read-only picture of how
// this running API is doing and how it is set up, for an administrator who
// would otherwise have to log into the server to find out — which version is
// deployed, whether the database answers and how far its migrations are,
// whether email and Telegram are configured, which timeouts are in force.
// Nothing here changes anything, and nothing secret is ever included: no
// tokens, no passwords, no connection strings.
package adminstatus

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

// Info is everything about the process that is known when it starts and
// doesn't change, handed over by cmd/api — what is configured and how it was
// built. What can change while it runs (the database answering, a directory
// existing) is looked at on each request instead.
type Info struct {
	StartedAt time.Time

	PublicBaseURL         string
	SessionTTL            time.Duration
	MagicLinkTTL          time.Duration
	MagicLinkCooldown     time.Duration
	ImpersonationTTL      time.Duration
	TelegramBotDMLinkTTL  time.Duration
	SMTPConfigured        bool
	TelegramLogConfigured bool
	TelegramBotRunning    bool
	TelegramBotUsername   string

	LibraryRoot           string
	UserFilesRoot         string
	ActivityResourcesRoot string
}

type Handler struct {
	pool          db.PGX
	authenticator *auth.Authenticator
	info          Info
}

func NewHandler(pool db.PGX, authenticator *auth.Authenticator, info Info) *Handler {
	return &Handler{pool: pool, authenticator: authenticator, info: info}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/admin/status", h.authenticator.Protect(h.status, "admin"))
}

type appStatus struct {
	Version     string    `json:"version,omitempty"`
	VCSRevision string    `json:"vcs_revision,omitempty"`
	VCSTime     string    `json:"vcs_time,omitempty"`
	VCSModified bool      `json:"vcs_modified,omitempty"`
	GoVersion   string    `json:"go_version"`
	StartedAt   time.Time `json:"started_at"`
}

type databaseStatus struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	// MigrationVersion is the latest goose migration applied, absent when
	// the database can't be asked (or has no goose version table at all).
	MigrationVersion *int64 `json:"migration_version,omitempty"`
}

type telegramStatus struct {
	LogChannelConfigured bool   `json:"log_channel_configured"`
	BotRunning           bool   `json:"bot_running"`
	BotUsername          string `json:"bot_username,omitempty"`
}

type settingsStatus struct {
	PublicBaseURL            string `json:"public_base_url"`
	SessionTTLSeconds        int64  `json:"session_ttl_seconds"`
	MagicLinkTTLSeconds      int64  `json:"magic_link_ttl_seconds"`
	MagicLinkCooldownSeconds int64  `json:"magic_link_cooldown_seconds"`
	ImpersonationTTLSeconds  int64  `json:"impersonation_ttl_seconds"`
	BotDMLinkTTLSeconds      int64  `json:"bot_dm_link_ttl_seconds"`
}

type storageStatus struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type statusResponse struct {
	App      appStatus       `json:"app"`
	Database databaseStatus  `json:"database"`
	Mail     map[string]bool `json:"mail"`
	Telegram telegramStatus  `json:"telegram"`
	Settings settingsStatus  `json:"settings"`
	Storage  []storageStatus `json:"storage"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, statusResponse{
		App:      h.appStatus(),
		Database: h.databaseStatus(r.Context()),
		Mail:     map[string]bool{"smtp_configured": h.info.SMTPConfigured},
		Telegram: telegramStatus{
			LogChannelConfigured: h.info.TelegramLogConfigured,
			BotRunning:           h.info.TelegramBotRunning,
			BotUsername:          h.info.TelegramBotUsername,
		},
		Settings: settingsStatus{
			PublicBaseURL:            h.info.PublicBaseURL,
			SessionTTLSeconds:        int64(h.info.SessionTTL.Seconds()),
			MagicLinkTTLSeconds:      int64(h.info.MagicLinkTTL.Seconds()),
			MagicLinkCooldownSeconds: int64(h.info.MagicLinkCooldown.Seconds()),
			ImpersonationTTLSeconds:  int64(h.info.ImpersonationTTL.Seconds()),
			BotDMLinkTTLSeconds:      int64(h.info.TelegramBotDMLinkTTL.Seconds()),
		},
		Storage: []storageStatus{
			directory("library", h.info.LibraryRoot),
			directory("user_files", h.info.UserFilesRoot),
			directory("activity_resources", h.info.ActivityResourcesRoot),
		},
	})
}

// appStatus adds what the Go toolchain recorded in the binary (the commit it
// was built from, when that is known) to what cmd/api passed in.
func (h *Handler) appStatus() appStatus {
	app := appStatus{GoVersion: runtime.Version(), StartedAt: h.info.StartedAt}
	if build, ok := debug.ReadBuildInfo(); ok {
		app.Version = moduleVersion(build)
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				app.VCSRevision = setting.Value
			case "vcs.time":
				app.VCSTime = setting.Value
			case "vcs.modified":
				app.VCSModified = setting.Value == "true"
			}
		}
	}
	return app
}

// Build is what Go recorded about the binary: the main module's version and
// when the commit it was built from was made. Either is the zero value when
// unknown (a build outside a git checkout, `go run`, a test).
type Build struct {
	Version    string
	CommitTime time.Time
}

// ReadBuild reads Build from the running binary.
func ReadBuild() Build {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Build{}
	}
	return buildFrom(info)
}

func buildFrom(info *debug.BuildInfo) Build {
	build := Build{Version: moduleVersion(info)}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.time" {
			if t, err := time.Parse(time.RFC3339, setting.Value); err == nil {
				build.CommitTime = t
			}
		}
	}
	return build
}

// moduleVersion is the version Go recorded for the main module
// (debug.BuildInfo.Main.Version): a tag when built from a tagged commit, else
// a pseudo-version with the commit and, when the tree had changes, "+dirty".
// Go itself writes "(devel)" when it has nothing to record (a build outside a
// repository, `go run`, tests), which is no version, so it comes out empty.
func moduleVersion(build *debug.BuildInfo) string {
	if build.Main.Version == "(devel)" {
		return ""
	}
	return build.Main.Version
}

// databaseStatus times a trivial query, and asks goose's own table how far
// the migrations have got. A database that doesn't answer is reported, not
// an error of this endpoint: that is exactly what it is here to show.
func (h *Handler) databaseStatus(ctx context.Context) databaseStatus {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	start := time.Now()
	var one int
	if err := h.pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return databaseStatus{Error: err.Error()}
	}
	status := databaseStatus{OK: true, LatencyMS: time.Since(start).Milliseconds()}

	// Its own subtransaction: under a transaction, as in the tests, a query
	// against a table that doesn't exist would otherwise leave it aborted.
	if tx, err := h.pool.Begin(ctx); err == nil {
		defer tx.Rollback(ctx)
		var version *int64
		if err := tx.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err == nil {
			status.MigrationVersion = version
		}
	}
	return status
}

func directory(name, path string) storageStatus {
	info, err := os.Stat(path)
	return storageStatus{Name: name, Path: path, Exists: err == nil && info.IsDir()}
}

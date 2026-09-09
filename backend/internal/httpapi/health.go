package httpapi

import (
	"context"
	"net/http"
	"time"
)

// dbPinger is the one piece of *pgxpool.Pool a health check needs — kept
// as a tiny local interface, the same "define just what's used,
// satisfied structurally" pattern this codebase already uses elsewhere
// (UserLookup, TelegramMessenger, ...), rather than importing pgxpool
// (or even platform/db) directly into this transport-level package.
type dbPinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler answers GET /health with whether the process is up and,
// separately, whether it can currently reach Postgres — unauthenticated,
// since a reverse proxy or systemd unit polling this has no session
// token to send, and there's nothing here worth protecting anyway.
type HealthHandler struct {
	db dbPinger
}

func NewHealthHandler(db dbPinger) *HealthHandler {
	return &HealthHandler{db: db}
}

func (h *HealthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", h.check)
}

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// A short, fixed timeout rather than inheriting the request's own
// context unbounded — a caller polling this endpoint shouldn't have to
// guess how long to wait before deciding the process itself is stuck.
const healthCheckTimeout = 2 * time.Second

func (h *HealthHandler) check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		WriteJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "error", Database: "unreachable"})
		return
	}
	WriteJSON(w, http.StatusOK, healthResponse{Status: "ok", Database: "ok"})
}

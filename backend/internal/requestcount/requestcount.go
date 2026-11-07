// Package requestcount counts the requests the API answers, per minute, and keeps the counts in
// the database (table request_counts) so that /admin can say how many there were in the last hour,
// day and month. Counting is in memory, one increment per request; a background loop adds what has
// accumulated to the table every few seconds, so a request costs no query and a restart loses at
// most the last few seconds.
package requestcount

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

// healthPath is left out of the counts: whatever polls it (a monitor, the proxy) is not people
// using the intranet.
const healthPath = "/api/v1/health"

// flushEvery is how often the counts are added to the table.
const flushEvery = 15 * time.Second

type Counter struct {
	pool db.PGX
	now  func() time.Time

	mu     sync.Mutex
	counts map[time.Time]int64 // the start of each minute -> requests in it, not yet stored
}

func New(pool db.PGX) *Counter {
	return &Counter{pool: pool, now: time.Now, counts: map[time.Time]int64{}}
}

// counted says whether a request is one to count: anything under /api/ but the health check.
func counted(path string) bool {
	return strings.HasPrefix(path, "/api/") && path != healthPath
}

// Middleware counts every request it sees that deserves it, when it arrives (so the ones that fail,
// are refused or do not exist count too), and passes it on.
func (c *Counter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if counted(r.URL.Path) {
			c.add(c.now())
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Counter) add(at time.Time) {
	minute := at.Truncate(time.Minute)
	c.mu.Lock()
	c.counts[minute]++
	c.mu.Unlock()
}

// Flush adds what has been counted since the last flush to the table. If it fails the counts stay
// in memory and go in with the next one.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	pending := c.counts
	c.counts = map[time.Time]int64{}
	c.mu.Unlock()

	var failed error
	for minute, requests := range pending {
		_, err := c.pool.Exec(ctx, `
			INSERT INTO request_counts (minute, requests) VALUES ($1, $2)
			ON CONFLICT (minute) DO UPDATE SET requests = request_counts.requests + EXCLUDED.requests
		`, minute, requests)
		if err != nil {
			failed = err
			c.mu.Lock()
			c.counts[minute] += requests
			c.mu.Unlock()
		}
	}
	return failed
}

// Run flushes every few seconds until ctx is done, and once more then.
func (c *Counter) Run(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.Flush(flushCtx); err != nil {
				slog.Error("store the request counts", "error", err)
			}
			cancel()
			return
		case <-ticker.C:
			if err := c.Flush(ctx); err != nil {
				slog.Error("store the request counts", "error", err)
			}
		}
	}
}

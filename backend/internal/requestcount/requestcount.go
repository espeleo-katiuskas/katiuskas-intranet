// Package requestcount counts the requests the API answers and keeps the counts in the database, so
// that /admin can say how many there were in the last hour, day and month (table request_counts, per
// minute) and who used the API each day (table user_activity, per person and day, for the calendar of
// activity). Counting is in memory, one increment per request; a background loop adds what has
// accumulated to the tables every few minutes, so a request costs no query. What is still in memory is
// stored when the API shuts down in an orderly way (cmd/api) and, for /admin, whenever the figures are
// read; only a crash or a kill loses the last few minutes.
package requestcount

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the club's time zone must be there whatever the host has installed

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

// healthPath is left out of the counts: whatever polls it (a monitor, the proxy) is not people
// using the intranet.
const healthPath = "/api/v1/health"

// flushEvery is how often the counts are added to the tables.
const flushEvery = 5 * time.Minute

// foreignKeyViolationCode is what Postgres raises for activity of a person who has been deleted.
const foreignKeyViolationCode = "23503"

// clubZone is the time zone whose midnight ends a day of activity: the club's, not the server's.
var clubZone = mustLoadZone("Europe/Madrid")

func mustLoadZone(name string) *time.Location {
	zone, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return zone
}

// Day is the calendar day, "2026-11-07", that a moment falls on in the club's time zone.
func Day(at time.Time) string {
	return at.In(clubZone).Format("2006-01-02")
}

// personDay is a person's activity on one day.
type personDay struct {
	day    string
	userID int64
}

type Counter struct {
	pool db.PGX
	now  func() time.Time

	mu      sync.Mutex
	counts  map[time.Time]int64 // the start of each minute -> requests in it, not yet stored
	persons map[personDay]int64 // a person and a day -> their requests, not yet stored
}

func New(pool db.PGX) *Counter {
	return &Counter{pool: pool, now: time.Now, counts: map[time.Time]int64{}, persons: map[personDay]int64{}}
}

// counted says whether a request is one to count: anything under /api/ but the health check.
func counted(path string) bool {
	return strings.HasPrefix(path, "/api/") && path != healthPath
}

// Middleware counts every request it sees that deserves it, when it arrives (so the ones that fail,
// are refused or do not exist count too), and passes it on. Once it has been answered, a request by
// somebody who signed in also counts for that person on that day — unless an administrator was acting
// as them, which is not their activity.
func (c *Counter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !counted(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		at := c.now()
		c.add(at)
		ctx, actor := httpapi.WithActorBox(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
		if actor.Known && !actor.Impersonated {
			c.addPerson(at, actor.UserID)
		}
	})
}

func (c *Counter) add(at time.Time) {
	minute := at.Truncate(time.Minute)
	c.mu.Lock()
	c.counts[minute]++
	c.mu.Unlock()
}

func (c *Counter) addPerson(at time.Time, userID int64) {
	key := personDay{day: Day(at), userID: userID}
	c.mu.Lock()
	c.persons[key]++
	c.mu.Unlock()
}

// Flush adds what has been counted since the last flush to the tables. What cannot be stored stays in
// memory and goes in with the next flush — except the activity of a person who no longer exists.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	pending, pendingPersons := c.counts, c.persons
	c.counts, c.persons = map[time.Time]int64{}, map[personDay]int64{}
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
	for key, requests := range pendingPersons {
		// In its own (sub)transaction, so that a person who is gone (a foreign key violation) does not
		// leave the rest of the flush in an aborted transaction.
		err := c.storePerson(ctx, key, requests)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
			continue
		}
		if err != nil {
			failed = err
			c.mu.Lock()
			c.persons[key] += requests
			c.mu.Unlock()
		}
	}
	return failed
}

func (c *Counter) storePerson(ctx context.Context, key personDay, requests int64) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_activity (day, user_id, requests) VALUES ($1::date, $2, $3)
		ON CONFLICT (day, user_id) DO UPDATE SET requests = user_activity.requests + EXCLUDED.requests
	`, key.day, key.userID, requests); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run flushes every few minutes until ctx is done. It does not flush when it stops: the requests
// still being answered then are counted already, but the last Flush is up to whoever shuts the
// server down, once those requests are over.
func (c *Counter) Run(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Flush(ctx); err != nil {
				slog.Error("store the request counts", "error", err)
			}
		}
	}
}

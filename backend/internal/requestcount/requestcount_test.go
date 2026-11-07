package requestcount

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func total(t *testing.T, c *Counter) (rows int, requests int64) {
	t.Helper()
	if err := c.pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(requests), 0) FROM request_counts`).Scan(&rows, &requests); err != nil {
		t.Fatal(err)
	}
	return rows, requests
}

// Only what is under /api/ counts, the health check apart, whatever the answer; the counts go to the
// table per minute and add up over several flushes.
func TestRequestsAreCountedPerMinuteAndStored(t *testing.T) {
	c := New(testutil.NewTx(t))
	at := time.Date(2026, 11, 7, 12, 30, 15, 0, time.UTC)
	c.now = func() time.Time { return at }
	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/missing" {
			http.NotFound(w, r)
		}
	}))
	for _, path := range []string{"/api/v1/users", "/api/v1/users", "/api/v1/missing", "/api/v1/health", "/login.html", "/api"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows, requests := total(t, c); rows != 1 || requests != 3 {
		t.Fatalf("expected 3 requests in one minute, got %d in %d rows", requests, rows)
	}

	// Later in the same minute, and in the next one.
	c.now = func() time.Time { return at.Add(30 * time.Second) }
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	c.now = func() time.Time { return at.Add(2 * time.Minute) }
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := c.pool.QueryRow(context.Background(), `SELECT requests FROM request_counts WHERE minute = $1`, at.Truncate(time.Minute)).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if rows, requests := total(t, c); rows != 2 || requests != 5 || first != 4 {
		t.Errorf("expected 5 requests in two minutes (4 in the first), got %d in %d rows (%d in the first)", requests, rows, first)
	}
	// Nothing left to store: flushing again changes nothing.
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows, requests := total(t, c); rows != 2 || requests != 5 {
		t.Errorf("a second flush changed the counts: %d in %d rows", requests, rows)
	}
}

// Run stops when its context is cancelled and does not store anything then: the last flush is the
// shutdown's to make, once the requests still going on are over.
func TestRunStopsWithoutFlushing(t *testing.T) {
	c := New(testutil.NewTx(t))
	c.add(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
	if rows, _ := total(t, c); rows != 0 {
		t.Errorf("Run stored %d rows when stopping", rows)
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, requests := total(t, c); requests != 1 {
		t.Errorf("the request counted was not kept for the final flush: %d stored", requests)
	}
}

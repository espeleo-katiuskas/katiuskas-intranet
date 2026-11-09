package requestcount

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
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

func peopleOf(t *testing.T, c *Counter) map[string]int64 {
	t.Helper()
	rows, err := c.pool.Query(context.Background(), `SELECT day::text || ' ' || user_id::text, requests FROM user_activity`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var key string
		var requests int64
		if err := rows.Scan(&key, &requests); err != nil {
			t.Fatal(err)
		}
		got[key] = requests
	}
	return got
}

// A request by somebody who signed in counts for that person on that day (the club's day), an
// administrator acting as somebody counts for nobody, and neither does a request nobody signed in for.
func TestRequestsAreCountedPerPersonAndDay(t *testing.T) {
	tx := testutil.NewTx(t)
	c := New(tx)
	ana, beto := testutil.InsertMember(t, tx), testutil.InsertMember(t, tx)
	var actor *httpapi.Actor
	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actor != nil {
			httpapi.SetRequestActor(r.Context(), actor.UserID, actor.Impersonated)
		}
	}))
	request := func(at time.Time, who *httpapi.Actor) {
		c.now = func() time.Time { return at }
		actor = who
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	}
	// 23:30 UTC on the 6th is already the 7th in Madrid (UTC+1 in November): the club's day, not UTC's.
	lateOnThe6th := time.Date(2026, 11, 6, 23, 30, 0, 0, time.UTC)
	request(lateOnThe6th, &httpapi.Actor{UserID: ana.ID, Known: true})
	request(lateOnThe6th, &httpapi.Actor{UserID: ana.ID, Known: true})
	request(lateOnThe6th.Add(-2*time.Hour), &httpapi.Actor{UserID: ana.ID, Known: true}) // 21:30 UTC: the 6th
	request(lateOnThe6th, &httpapi.Actor{UserID: beto.ID, Known: true})
	request(lateOnThe6th, &httpapi.Actor{UserID: ana.ID, Impersonated: true, Known: true})
	request(lateOnThe6th, nil)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := peopleOf(t, c)
	want := map[string]int64{
		fmt.Sprintf("2026-11-06 %d", ana.ID):  1,
		fmt.Sprintf("2026-11-07 %d", ana.ID):  2,
		fmt.Sprintf("2026-11-07 %d", beto.ID): 1,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for key, requests := range want {
		if got[key] != requests {
			t.Errorf("%s: expected %d, got %d (all: %v)", key, requests, got[key], got)
		}
	}
	// The totals count every request of the six.
	if _, requests := total(t, c); requests != 6 {
		t.Errorf("expected 6 requests in total, got %d", requests)
	}

	// More later adds to the same row.
	request(lateOnThe6th, &httpapi.Actor{UserID: beto.ID, Known: true})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := peopleOf(t, c)[fmt.Sprintf("2026-11-07 %d", beto.ID)]; got != 2 {
		t.Errorf("a second flush should add to the day: %d", got)
	}
}

// The activity of somebody who has been deleted is dropped, not retried for ever, and does not stop
// the rest from being stored.
func TestFlushDropsTheActivityOfAPersonWhoIsGone(t *testing.T) {
	tx := testutil.NewTx(t)
	c := New(tx)
	ana := testutil.InsertMember(t, tx)
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	c.addPerson(at, 987654321) // nobody
	c.addPerson(at, ana.ID)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := peopleOf(t, c); len(got) != 1 {
		t.Errorf("expected only the person who exists, got %v", got)
	}
	if len(c.persons) != 0 {
		t.Errorf("the activity of the missing person was kept for the next flush: %v", c.persons)
	}
}

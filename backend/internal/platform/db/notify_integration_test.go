package db_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

type recordedMutation struct{ operation, table string }

type mutationRecorder struct {
	mu   sync.Mutex
	seen []recordedMutation
}

func (r *mutationRecorder) hook(_ context.Context, operation, table string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, recordedMutation{operation, table})
}

func (r *mutationRecorder) waitFor(t *testing.T, want recordedMutation) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.seen {
			if m == want {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %+v to be reported; saw %+v", want, r.seen)
}

// TestNotifyingPGXCoversStatementsInsideANestedTransaction is the case
// that matters most: a repository (activities, inventory,
// activityresources) that opens its own transaction via Begin must still
// have its mutations reported, not just ones run directly on the pool.
func TestNotifyingPGXCoversStatementsInsideANestedTransaction(t *testing.T) {
	tx := testutil.NewTx(t)
	recorder := &mutationRecorder{}
	conn := db.NewNotifyingPGX(tx, recorder.hook)

	// A mutation run directly.
	if _, err := conn.Exec(context.Background(), `
		INSERT INTO holidays (date, name) VALUES ('2030-01-01', 'Test Holiday')
	`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	recorder.waitFor(t, recordedMutation{"insert", "holidays"})

	// A mutation run inside a transaction the wrapper's own Begin opens —
	// exactly the shape activities/inventory/activityresources use.
	nested, err := conn.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := nested.Exec(context.Background(), `
		INSERT INTO holidays (date, name) VALUES ('2030-01-02', 'Test Holiday 2')
	`); err != nil {
		t.Fatalf("nested Exec: %v", err)
	}
	if err := nested.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	recorder.waitFor(t, recordedMutation{"insert", "holidays"})

	var count int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM holidays WHERE name LIKE 'Test Holiday%'`).Scan(&count); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if count != 2 {
		t.Errorf("expected both inserts to be visible, got count=%d", count)
	}
}

package db

import (
	"context"
	"testing"
)

func TestClassifyMutation(t *testing.T) {
	cases := []struct {
		name          string
		sql           string
		wantOperation string
		wantTable     string
		wantOK        bool
	}{
		{
			name:          "insert",
			sql:           "\n\t\tINSERT INTO users (first_name, last_name) VALUES ($1, $2)",
			wantOperation: "insert",
			wantTable:     "users",
			wantOK:        true,
		},
		{
			name:          "insert with table alias",
			sql:           "\n\t\tINSERT INTO users AS u (first_name) VALUES ($1) ON CONFLICT DO NOTHING RETURNING u.id",
			wantOperation: "insert",
			wantTable:     "users",
			wantOK:        true,
		},
		{
			name:          "update",
			sql:           "UPDATE activities SET title = $1 WHERE id = $2",
			wantOperation: "update",
			wantTable:     "activities",
			wantOK:        true,
		},
		{
			name:          "delete",
			sql:           "\n\t\tDELETE FROM item_movements WHERE id = $1",
			wantOperation: "delete",
			wantTable:     "item_movements",
			wantOK:        true,
		},
		{
			name:   "select is not a mutation",
			sql:    "SELECT id, first_name FROM users WHERE id = $1",
			wantOK: false,
		},
		{
			name:   "unrecognized statement",
			sql:    "SELECT pg_advisory_lock($1)",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operation, table, ok := classifyMutation(tc.sql)
			if ok != tc.wantOK {
				t.Fatalf("classifyMutation(%q) ok = %v, want %v", tc.sql, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if operation != tc.wantOperation || table != tc.wantTable {
				t.Errorf("classifyMutation(%q) = (%q, %q), want (%q, %q)",
					tc.sql, operation, table, tc.wantOperation, tc.wantTable)
			}
		})
	}
}

func TestDispatchMutationHookCallsHookForMutationsOnly(t *testing.T) {
	calls := make(chan struct{ operation, table string }, 1)
	hook := func(_ context.Context, operation, table string) {
		calls <- struct{ operation, table string }{operation, table}
	}

	dispatchMutationHook(hook, "SELECT 1")
	select {
	case c := <-calls:
		t.Fatalf("hook should not fire for a SELECT, got %+v", c)
	default:
	}

	dispatchMutationHook(hook, "INSERT INTO holidays (date, name) VALUES ($1, $2)")
	c := <-calls
	if c.operation != "insert" || c.table != "holidays" {
		t.Errorf("got %+v, want insert/holidays", c)
	}
}

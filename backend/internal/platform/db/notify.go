package db

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MutationHook is called after a statement that looks like a mutation
// (insert/update/delete) executes successfully — see NewNotifyingPGX.
// operation is lowercase ("insert"/"update"/"delete"); table is the
// target table's name. Always called on its own goroutine with a
// bounded timeout on a background context (see NotifyingPGX.notify), so
// a slow implementation never adds latency to whatever triggered the
// write, and a cancelled request context never cuts the hook short.
type MutationHook func(ctx context.Context, operation, table string)

// mutationPattern picks out the operation and the table a SQL statement
// targets from its very start — good enough for every query this
// codebase writes by hand (see internal/*/repository.go: always
// "INSERT INTO table", "UPDATE table", or "DELETE FROM table" at the
// start of the statement, whitespace aside), not a general-purpose SQL
// parser. A statement it doesn't recognize — every SELECT, or DDL run by
// migrations, which never go through this interface at all — simply
// isn't a mutation and triggers no hook call.
var mutationPattern = regexp.MustCompile(`(?is)^\s*(?:(insert)\s+into|(update)|(delete)\s+from)\s+([a-z_][a-z0-9_]*)`)

func classifyMutation(sql string) (operation, table string, ok bool) {
	m := mutationPattern.FindStringSubmatch(sql)
	if m == nil {
		return "", "", false
	}
	for _, op := range m[1:4] {
		if op != "" {
			return strings.ToLower(op), m[4], true
		}
	}
	return "", "", false
}

// NotifyingPGX wraps a PGX, calling hook for every statement that looks
// like a mutation — including one issued through QueryRow, since this
// codebase's usual shape for a single-row write is "INSERT/UPDATE ...
// RETURNING", which never goes through Exec at all (see CLAUDE.md).
// Begin returns a notifyingTx that keeps applying the same hook to
// whatever runs inside that transaction, so a repository that manages
// its own multi-statement transaction (activities, inventory,
// activityresources) is covered exactly the same way as one that isn't.
type NotifyingPGX struct {
	next PGX
	hook MutationHook
}

func NewNotifyingPGX(next PGX, hook MutationHook) *NotifyingPGX {
	return &NotifyingPGX{next: next, hook: hook}
}

var (
	_ PGX    = (*NotifyingPGX)(nil)
	_ pgx.Tx = (*notifyingTx)(nil)
	_ PGX    = (*notifyingTx)(nil)
)

// dispatchMutationHook classifies sql and, if it looks like a mutation,
// calls hook on its own goroutine with a bounded timeout on a background
// context — shared by NotifyingPGX and notifyingTx below.
func dispatchMutationHook(hook MutationHook, sql string) {
	operation, table, ok := classifyMutation(sql)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hook(ctx, operation, table)
	}()
}

func (n *NotifyingPGX) notify(sql string) {
	dispatchMutationHook(n.hook, sql)
}

func (n *NotifyingPGX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := n.next.Exec(ctx, sql, args...)
	if err == nil {
		n.notify(sql)
	}
	return tag, err
}

func (n *NotifyingPGX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := n.next.Query(ctx, sql, args...)
	if err == nil {
		n.notify(sql)
	}
	return rows, err
}

func (n *NotifyingPGX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := n.next.QueryRow(ctx, sql, args...)
	// QueryRow never reports an error itself (that's deferred to
	// Scan on the returned Row), so this fires whenever the statement
	// looks like a mutation, whether or not the row is later found
	// missing — good enough for "something changed", not a
	// commit-level guarantee.
	n.notify(sql)
	return row
}

func (n *NotifyingPGX) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := n.next.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &notifyingTx{Tx: tx, hook: n.hook}, nil
}

// notifyingTx applies NotifyingPGX's same mutation detection to
// statements run inside a transaction a repository opened itself via
// Begin. Embedding the real pgx.Tx promotes every method this doesn't
// override (Commit, Rollback, CopyFrom, ...) unchanged.
type notifyingTx struct {
	pgx.Tx
	hook MutationHook
}

func (t *notifyingTx) notify(sql string) {
	dispatchMutationHook(t.hook, sql)
}

func (t *notifyingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := t.Tx.Exec(ctx, sql, args...)
	if err == nil {
		t.notify(sql)
	}
	return tag, err
}

func (t *notifyingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := t.Tx.Query(ctx, sql, args...)
	if err == nil {
		t.notify(sql)
	}
	return rows, err
}

func (t *notifyingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := t.Tx.QueryRow(ctx, sql, args...)
	t.notify(sql)
	return row
}

func (t *notifyingTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := t.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &notifyingTx{Tx: tx, hook: t.hook}, nil
}

package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PGX is the subset of *pgxpool.Pool every repository actually uses.
// Both *pgxpool.Pool and pgx.Tx satisfy it, so a repository built with a
// pool in production can be built with a transaction in tests instead —
// each test gets its own transaction, rolled back on cleanup, without the
// repository code ever knowing the difference.
type PGX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

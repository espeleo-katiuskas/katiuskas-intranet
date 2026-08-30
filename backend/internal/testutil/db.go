// Package testutil sets up an isolated PostgreSQL transaction for tests
// that need a real database — every repository in this backend talks to
// Postgres directly (exclusion constraints, CHECK constraints and unique
// violations are part of the actual behavior being tested, not something
// worth mocking away).
//
// Point TEST_DATABASE_URL at a throwaway database (never the dev or
// production one) and NewTx will apply db/migrations/*.sql to it once,
// then hand each test its own transaction that's rolled back on cleanup —
// so tests never see each other's data and can run with t.Parallel().
// Without TEST_DATABASE_URL set (and reachable), tests using this package
// skip instead of failing, so `go test ./...` still works on a machine
// with no Postgres at all.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTestDSN = "postgres://intranet:intranet@localhost:5433/intranet_test?sslmode=disable"

// schemaLockKey is an arbitrary constant used as a Postgres advisory lock
// so that concurrent test binaries (each Go package's tests run in their
// own process) don't race to apply migrations against the same database.
const schemaLockKey = 918_273_645

var (
	setupOnce sync.Once
	setupPool *pgxpool.Pool
	setupErr  error
)

func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDSN
}

// NewTx returns a transaction against a fully migrated test database, with
// automatic rollback registered via t.Cleanup. It calls t.Skip if no test
// database is reachable.
func NewTx(t *testing.T) pgx.Tx {
	t.Helper()
	ctx := context.Background()

	pool, err := ensureSchema(ctx)
	if err != nil {
		t.Skipf("skipping: test database not available at %s (set TEST_DATABASE_URL): %v", testDSN(), err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
	})
	return tx
}

func ensureSchema(ctx context.Context) (*pgxpool.Pool, error) {
	setupOnce.Do(func() {
		pool, err := pgxpool.New(ctx, testDSN())
		if err != nil {
			setupErr = fmt.Errorf("connect: %w", err)
			return
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			setupErr = fmt.Errorf("ping: %w", err)
			return
		}

		if err := migrateIfNeeded(ctx, pool); err != nil {
			pool.Close()
			setupErr = err
			return
		}
		setupPool = pool
	})
	return setupPool, setupErr
}

// migrateIfNeeded applies every migration exactly once across however many
// test-binary processes are hitting this database concurrently: it holds a
// session-scoped advisory lock while it checks and, if needed, migrates,
// so a second process always sees the finished result instead of racing
// the first one's DDL.
func migrateIfNeeded(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(schemaLockKey)); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(schemaLockKey))

	var migrated bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'magic_link_tokens')
	`).Scan(&migrated)
	if err != nil {
		return fmt.Errorf("check schema: %w", err)
	}
	if migrated {
		return nil
	}

	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		up, err := upSection(string(content))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, stmt := range sqlStatements(up) {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w\nstatement:\n%s", path, err, stmt)
			}
		}
	}
	return nil
}

// migrationFiles locates db/migrations relative to this source file, so it
// resolves the same way regardless of which package's tests call NewTx.
func migrationFiles() ([]string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("could not determine testutil source location")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "migrations")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths, nil
}

func upSection(content string) (string, error) {
	const upMarker = "-- +goose Up"
	const downMarker = "-- +goose Down"
	start := strings.Index(content, upMarker)
	if start == -1 {
		return "", fmt.Errorf("no %q marker found", upMarker)
	}
	rest := content[start+len(upMarker):]
	if end := strings.Index(rest, downMarker); end != -1 {
		rest = rest[:end]
	}
	return rest, nil
}

// sqlStatements splits a block of SQL into individual statements. It
// strips "--" line comments first — a semicolon inside one (e.g. "can't
// be deleted; see below") would otherwise look like a statement
// terminator. None of these migrations put a semicolon inside a string
// literal, so a plain split on ";" is otherwise enough.
func sqlStatements(sql string) []string {
	var statements []string
	for _, raw := range strings.Split(stripLineComments(sql), ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		statements = append(statements, stmt)
	}
	return statements
}

func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx != -1 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// UniqueEmail returns a per-test, per-call unique email address, so
// inserting a users row never collides with another test — including one
// running concurrently in a different package's test binary against the
// same shared test database.
func UniqueEmail(t *testing.T) string {
	t.Helper()
	slug := strings.ToLower(nonAlnum.ReplaceAllString(t.Name(), "-"))
	return fmt.Sprintf("%s-%d@test.example", slug, time.Now().UnixNano())
}

// TestUser is a minimal person row inserted for tests that need a real
// users.id to satisfy a foreign key (role assignment, activity
// participation, a membership period, and so on).
type TestUser struct {
	ID    int64
	Email string
	Phone string
}

// InsertUser inserts a bare-bones person. Pass phone as "" to leave it
// NULL — FindActiveByEmailAndPhone and the magic-link flow both require
// one, since matching email *and* phone is the only thing gating a
// sign-in link now (there's no separate "granted access" flag or row).
func InsertUser(t *testing.T, tx pgx.Tx, phone string) TestUser {
	t.Helper()
	ctx := context.Background()

	email := UniqueEmail(t)
	var phoneArg any
	if phone != "" {
		phoneArg = phone
	}

	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO users (first_name, last_name, email, phone) VALUES ('Test', 'User', $1, $2)
		RETURNING id
	`, email, phoneArg).Scan(&id)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	return TestUser{ID: id, Email: email, Phone: phone}
}

// InsertActivity inserts a bare-bones activity, for tests that need a real
// activities.id to satisfy a foreign key — namely a loan or rental
// transaction, which the loan_and_rental_require_activity CHECK constraint
// (migration 00006) requires.
func InsertActivity(t *testing.T, tx pgx.Tx) int64 {
	t.Helper()
	leader := InsertUser(t, tx, "")
	var id int64
	err := tx.QueryRow(context.Background(), `
		INSERT INTO activities (title, type, start_date, location, leader_id)
		VALUES ('Test activity', 'caving', now(), 'Test location', $1)
		RETURNING id
	`, leader.ID).Scan(&id)
	if err != nil {
		t.Fatalf("insert test activity: %v", err)
	}
	return id
}

// RoleID looks up a system role's id by name (they're seeded by migration
// 00003: "admin", "board", "member", "former_member").
func RoleID(t *testing.T, tx pgx.Tx, name string) int64 {
	t.Helper()
	var id int64
	err := tx.QueryRow(context.Background(), `SELECT id FROM roles WHERE name = $1`, name).Scan(&id)
	if err != nil {
		t.Fatalf("look up role %q: %v", name, err)
	}
	return id
}

// LicenseTypeID looks up a catalog license type's id by name (seeded by
// migration 00017: "A BASICA", "B1 PLUS", "Otra", etc).
func LicenseTypeID(t *testing.T, tx pgx.Tx, name string) int64 {
	t.Helper()
	var id int64
	err := tx.QueryRow(context.Background(), `SELECT id FROM license_types WHERE name = $1`, name).Scan(&id)
	if err != nil {
		t.Fatalf("look up license type %q: %v", name, err)
	}
	return id
}

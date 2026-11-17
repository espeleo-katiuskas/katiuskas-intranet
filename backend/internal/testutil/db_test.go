package testutil

import (
	"context"
	"testing"
)

func TestNewTxAppliesMigrationsAndIsolatesTests(t *testing.T) {
	tx := NewTx(t)

	var count int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM roles WHERE is_system`).Scan(&count); err != nil {
		t.Fatalf("query roles: %v", err)
	}
	if count != 4 {
		t.Fatalf("expected 4 system roles seeded, got %d", count)
	}

	user := InsertUser(t, tx, "600111222")
	if user.ID == 0 {
		t.Fatalf("expected a non-zero user id")
	}

	adminRoleID := RoleID(t, tx, "admin")
	if adminRoleID == 0 {
		t.Fatalf("expected a non-zero admin role id")
	}
}

func TestNewTxRollsBackBetweenTests(t *testing.T) {
	tx := NewTx(t)

	var count int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no leftover users from other tests, found %d", count)
	}
}

// TestSqlStatementsHandlesStatementBeginEndBlocks reproduces a real bug:
// migrateIfNeeded's plain semicolon-splitting broke on
// 00012_accounting.sql's own StatementBegin/StatementEnd-wrapped trigger
// function ("unterminated dollar-quoted string"), since the ";"s inside
// the $$-quoted body look like statement terminators to a splitter that
// doesn't know about goose's own markers for exactly this case. That
// only ever surfaced against a genuinely fresh database — every
// developer's already-migrated intranet_test short-circuits past the
// whole migration loop (see migrateIfNeeded's own "already migrated"
// check) — which is exactly what CI's own fresh Postgres service hits
// on every single run.
func TestSqlStatementsHandlesStatementBeginEndBlocks(t *testing.T) {
	input := `CREATE TABLE foo (id int);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bar() RETURNS trigger AS $$
DECLARE
    x int := 1;
BEGIN
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE INDEX idx_foo ON foo(id);
`
	got := sqlStatements(input)
	want := []string{
		"CREATE TABLE foo (id int)",
		"CREATE OR REPLACE FUNCTION bar() RETURNS trigger AS $$\nDECLARE\n    x int := 1;\nBEGIN\n    RETURN NULL;\nEND;\n$$ LANGUAGE plpgsql;",
		"CREATE INDEX idx_foo ON foo(id)",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d statements, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d:\n got:  %q\n want: %q", i, got[i], want[i])
		}
	}
}

// setNull are the foreign keys that are ON DELETE SET NULL on purpose: what they hold is derived and is worked out
// again (the zone of an element of the OpenStreetMap mirror, which cmd/osmsync sets on each load), so deleting
// the thing they point at must not be stopped by them. Each one is named here, with its reason, and no other
// may be.
var setNull = map[string]bool{
	"osm_elements.osm_elements_area_id_fkey": true,
}

// One rule for every foreign key: ON DELETE RESTRICT. Nothing cascades and nothing is set to NULL,
// so deleting something never takes anything else with it unless the code does it, explicitly. The only
// exceptions are those of setNull.
func TestEveryForeignKeyIsRestrict(t *testing.T) {
	tx := NewTx(t)
	rows, err := tx.Query(context.Background(), `
		SELECT conrelid::regclass::text, conname, confdeltype::text FROM pg_constraint
		WHERE contype = 'f' AND confdeltype <> 'r' ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, name, action string
		if err := rows.Scan(&table, &name, &action); err != nil {
			t.Fatal(err)
		}
		if setNull[table+"."+name] && action == "n" { // 'n' is SET NULL: not a cascade either.
			continue
		}
		t.Errorf("%s.%s is not ON DELETE RESTRICT", table, name)
	}
}

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

	user := InsertUser(t, tx, "600111222", true)
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

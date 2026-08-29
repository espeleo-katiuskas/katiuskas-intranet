package roles_test

import (
	"context"
	"errors"
	"testing"

	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
)

func TestListIncludesSeededSystemRoles(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))

	list, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var systemNames []string
	for _, r := range list {
		if r.IsSystem {
			systemNames = append(systemNames, r.Name)
		}
	}
	want := map[string]bool{"admin": true, "board": true, "member": true}
	if len(systemNames) != len(want) {
		t.Fatalf("expected 3 system roles, got %v", systemNames)
	}
	for _, name := range systemNames {
		if !want[name] {
			t.Errorf("unexpected system role %q", name)
		}
	}
}

func TestCreateAndGetRole(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))
	ctx := context.Background()

	desc := "Encargado del material del club"
	created, err := repo.Create(ctx, roles.CreateInput{Name: "material", Description: &desc})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.IsSystem {
		t.Errorf("expected a newly created role not to be a system role")
	}

	byID, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if byID.Name != "material" {
		t.Errorf("expected name %q, got %q", "material", byID.Name)
	}

	byName, err := repo.GetByName(ctx, "material")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("expected same id looking up by name, got %d vs %d", byName.ID, created.ID)
	}
}

func TestCreateDuplicateNameRejected(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))
	ctx := context.Background()

	if _, err := repo.Create(ctx, roles.CreateInput{Name: "instructor"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(ctx, roles.CreateInput{Name: "instructor"})
	if !errors.Is(err, roles.ErrDuplicateName) {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))
	if _, err := repo.GetByID(context.Background(), 999999); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteRefusesSystemRoles(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	ctx := context.Background()

	adminID := testutil.RoleID(t, tx, "admin")
	if err := repo.Delete(ctx, adminID); !errors.Is(err, roles.ErrSystemRole) {
		t.Errorf("expected ErrSystemRole, got %v", err)
	}
}

func TestDeleteCustomRole(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))
	ctx := context.Background()

	created, err := repo.Create(ctx, roles.CreateInput{Name: "temporary"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected role to be gone, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	repo := roles.NewRepository(testutil.NewTx(t))
	if err := repo.Delete(context.Background(), 999999); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAssignAndListNamesForUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	memberID := testutil.RoleID(t, tx, "member")
	boardID := testutil.RoleID(t, tx, "board")

	if err := repo.Assign(ctx, user.ID, memberID); err != nil {
		t.Fatalf("assign member: %v", err)
	}
	if err := repo.Assign(ctx, user.ID, boardID); err != nil {
		t.Fatalf("assign board: %v", err)
	}
	// Assigning the same role twice should be a harmless no-op.
	if err := repo.Assign(ctx, user.ID, memberID); err != nil {
		t.Fatalf("re-assign member: %v", err)
	}

	names, err := repo.ListNamesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 2 || names[0] != "board" || names[1] != "member" {
		t.Errorf("expected [board member], got %v", names)
	}
}

// A failed statement leaves the rest of a Postgres transaction unusable
// until rollback, so each of these gets its own transaction rather than
// sharing one across both assertions.

func TestAssignUnknownUserFails(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	memberID := testutil.RoleID(t, tx, "member")

	if err := repo.Assign(context.Background(), 999999, memberID); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown user, got %v", err)
	}
}

func TestAssignUnknownRoleFails(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "", false)

	if err := repo.Assign(context.Background(), user.ID, 999999); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown role, got %v", err)
	}
}

func TestRemoveRole(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "", false)
	memberID := testutil.RoleID(t, tx, "member")
	if err := repo.Assign(ctx, user.ID, memberID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	if err := repo.Remove(ctx, user.ID, memberID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Removing again (already gone) should report not found.
	if err := repo.Remove(ctx, user.ID, memberID); !errors.Is(err, roles.ErrNotFound) {
		t.Errorf("expected ErrNotFound removing an unassigned role, got %v", err)
	}
}

func TestCountUsersWithRoleNameAndUserHasRoleName(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	ctx := context.Background()

	adminID := testutil.RoleID(t, tx, "admin")
	userA := testutil.InsertUser(t, tx, "", false)
	userB := testutil.InsertUser(t, tx, "", false)

	if count, err := repo.CountUsersWithRoleName(ctx, "admin"); err != nil || count != 0 {
		t.Fatalf("expected 0 admins initially, got %d (err %v)", count, err)
	}

	if err := repo.Assign(ctx, userA.ID, adminID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if count, err := repo.CountUsersWithRoleName(ctx, "admin"); err != nil || count != 1 {
		t.Fatalf("expected 1 admin, got %d (err %v)", count, err)
	}

	hasIt, err := repo.UserHasRoleName(ctx, userA.ID, "admin")
	if err != nil || !hasIt {
		t.Errorf("expected userA to have admin, got %v (err %v)", hasIt, err)
	}
	hasIt, err = repo.UserHasRoleName(ctx, userB.ID, "admin")
	if err != nil || hasIt {
		t.Errorf("expected userB not to have admin, got %v (err %v)", hasIt, err)
	}
}

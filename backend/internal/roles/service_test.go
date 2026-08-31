package roles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func newService(t *testing.T) (*roles.Service, testUserFactory) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	return roles.NewService(repo), func(phone string) testutil.TestUser {
		return testutil.InsertUser(t, tx, phone)
	}
}

type testUserFactory func(phone string) testutil.TestUser

func TestAssignByNameIsAllOrNothing(t *testing.T) {
	svc, newUser := newService(t)
	ctx := context.Background()
	user := newUser("")

	err := svc.AssignByName(ctx, user.ID, []string{"admin", "does-not-exist"})
	if !errors.Is(err, roles.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for the unknown role, got %v", err)
	}

	names, err := svc.ListNamesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected no roles assigned when one name in the batch is invalid, got %v", names)
	}
}

func TestAssignByNameSucceedsWhenAllNamesAreValid(t *testing.T) {
	svc, newUser := newService(t)
	ctx := context.Background()
	user := newUser("")

	if err := svc.AssignByName(ctx, user.ID, []string{"admin", "material"}); err != nil {
		t.Fatalf("assign by name: %v", err)
	}
	names, err := svc.ListNamesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list names: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 roles assigned, got %v", names)
	}
}

func TestAssignAndRemoveRejectAutomaticRoles(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	svc := roles.NewService(repo)
	ctx := context.Background()

	user := testutil.InsertUser(t, tx, "")
	memberID := testutil.RoleID(t, tx, "member")
	formerMemberID := testutil.RoleID(t, tx, "former_member")

	if err := svc.Assign(ctx, user.ID, memberID); !errors.Is(err, roles.ErrAutomaticRole) {
		t.Errorf("expected ErrAutomaticRole assigning member, got %v", err)
	}
	if err := svc.Assign(ctx, user.ID, formerMemberID); !errors.Is(err, roles.ErrAutomaticRole) {
		t.Errorf("expected ErrAutomaticRole assigning former_member, got %v", err)
	}
	if err := svc.AssignByName(ctx, user.ID, []string{"member"}); !errors.Is(err, roles.ErrAutomaticRole) {
		t.Errorf("expected ErrAutomaticRole assigning member by name, got %v", err)
	}
	if err := svc.Remove(ctx, user.ID, memberID); !errors.Is(err, roles.ErrAutomaticRole) {
		t.Errorf("expected ErrAutomaticRole removing member, got %v", err)
	}
}

func TestRemoveBlocksStrippingTheLastAdmin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	svc := roles.NewService(repo)
	ctx := context.Background()

	adminID := testutil.RoleID(t, tx, "admin")
	solo := testutil.InsertUser(t, tx, "")
	if err := svc.Assign(ctx, solo.ID, adminID); err != nil {
		t.Fatalf("assign admin: %v", err)
	}

	if err := svc.Remove(ctx, solo.ID, adminID); !errors.Is(err, roles.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}

	// Once a second admin exists, removing the first one's admin role is fine.
	second := testutil.InsertUser(t, tx, "")
	if err := svc.Assign(ctx, second.ID, adminID); err != nil {
		t.Fatalf("assign second admin: %v", err)
	}
	if err := svc.Remove(ctx, solo.ID, adminID); err != nil {
		t.Fatalf("expected removal to succeed with a second admin present: %v", err)
	}
}

func TestRemoveNonAdminRoleIsUnaffectedByLastAdminCheck(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	svc := roles.NewService(repo)
	ctx := context.Background()

	materialID := testutil.RoleID(t, tx, "material")
	user := testutil.InsertUser(t, tx, "")
	if err := svc.Assign(ctx, user.ID, materialID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := svc.Remove(ctx, user.ID, materialID); err != nil {
		t.Errorf("expected removing a non-admin role to succeed even as the only holder, got %v", err)
	}
}

func TestIsLastAdmin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := roles.NewRepository(tx)
	svc := roles.NewService(repo)
	ctx := context.Background()

	adminID := testutil.RoleID(t, tx, "admin")
	solo := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")

	if is, err := svc.IsLastAdmin(ctx, solo.ID); err != nil || is {
		t.Fatalf("expected false before any admin exists, got %v (err %v)", is, err)
	}

	if err := svc.Assign(ctx, solo.ID, adminID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if is, err := svc.IsLastAdmin(ctx, solo.ID); err != nil || !is {
		t.Fatalf("expected true for the sole admin, got %v (err %v)", is, err)
	}
	if is, err := svc.IsLastAdmin(ctx, other.ID); err != nil || is {
		t.Fatalf("expected false for a non-admin, got %v (err %v)", is, err)
	}

	if err := svc.Assign(ctx, other.ID, adminID); err != nil {
		t.Fatalf("assign second admin: %v", err)
	}
	if is, err := svc.IsLastAdmin(ctx, solo.ID); err != nil || is {
		t.Fatalf("expected false once a second admin exists, got %v (err %v)", is, err)
	}
}

package users_test

import (
	"context"
	"errors"
	"testing"

	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/users"
)

func newUsersService(t *testing.T) (*users.Service, *roles.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	return users.NewService(users.NewRepository(tx), rolesService), rolesService
}

// "member" used to be the default role assigned at creation; now it's
// computed from membership history instead (see the roles package doc
// comment), so a brand-new person — who doesn't have one yet — starts
// with no roles at all unless some are explicitly given.
func TestServiceCreateWithNoRolesGrantsNothing(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(u.Roles) != 0 {
		t.Errorf("expected no roles for a brand-new person, got %v", u.Roles)
	}
}

func TestServiceCreateRejectsAutomaticRoleInRoleNames(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t), RoleNames: []string{"member"},
	})
	if !errors.Is(err, roles.ErrAutomaticRole) {
		t.Errorf("expected roles.ErrAutomaticRole, got %v", err)
	}
}

func TestServiceCreateWithExplicitRoles(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t), RoleNames: []string{"board"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(u.Roles) != 1 || u.Roles[0] != "board" {
		t.Errorf("expected role [board], got %v", u.Roles)
	}
}

func TestServiceCreateWithUnknownRoleFailsButKeepsThePerson(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()
	email := testutil.UniqueEmail(t)

	_, err := svc.Create(ctx, users.CreateInput{
		FirstName: "A", LastName: "B", Email: email, RoleNames: []string{"does-not-exist"},
	})
	if !errors.Is(err, roles.ErrNotFound) {
		t.Fatalf("expected roles.ErrNotFound, got %v", err)
	}

	found, err := svc.Get(ctx, mustID(t, svc, email))
	if err != nil {
		t.Fatalf("expected the person record to still exist despite the role assignment failing: %v", err)
	}
	if len(found.Roles) != 0 {
		t.Errorf("expected no roles assigned, got %v", found.Roles)
	}
}

func TestServiceDeleteBlocksTheLastAdmin(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Solo", LastName: "Admin", Email: testutil.UniqueEmail(t), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	if err := svc.Delete(ctx, admin.ID); !errors.Is(err, roles.ErrLastAdmin) {
		t.Errorf("expected roles.ErrLastAdmin, got %v", err)
	}
}

func TestServiceDeleteAllowsRemovingAnAdminWhenAnotherRemains(t *testing.T) {
	svc, _ := newUsersService(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, users.CreateInput{
		FirstName: "First", LastName: "Admin", Email: testutil.UniqueEmail(t), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if _, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Second", LastName: "Admin", Email: testutil.UniqueEmail(t), RoleNames: []string{"admin"},
	}); err != nil {
		t.Fatalf("create second admin: %v", err)
	}

	if err := svc.Delete(ctx, first.ID); err != nil {
		t.Errorf("expected deletion to succeed with a second admin present, got %v", err)
	}
}

// mustID re-fetches by email since Create didn't return an id when it
// failed partway through.
func mustID(t *testing.T, svc *users.Service, email string) int64 {
	t.Helper()
	list, err := svc.List(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, u := range list {
		if u.Email == email {
			return u.ID
		}
	}
	t.Fatalf("no user found with email %q", email)
	return 0
}

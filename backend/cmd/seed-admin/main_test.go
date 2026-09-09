package main

import (
	"context"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

// TestSeedAdminCreatesAdminAccountOnce covers the two things this
// command actually promises: it creates an admin account with the
// given details on the first call, and running it again with the same
// email — as every deploy does, migrated database or not — is a silent
// no-op rather than a duplicate-email error.
func TestSeedAdminCreatesAdminAccountOnce(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := users.NewRepository(tx)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	service := users.NewService(repo, rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(service)

	birthDate, err := time.Parse("2006-01-02", "1970-01-01")
	if err != nil {
		t.Fatalf("parse birth date: %v", err)
	}
	params := seedParams{
		Email:     testutil.UniqueEmail(t),
		Phone:     "600000000",
		FirstName: "Admin",
		LastName:  "Club",
		BirthDate: birthDate,
	}

	ctx := context.Background()
	if err := seedAdmin(ctx, repo, service, params); err != nil {
		t.Fatalf("1st seedAdmin call: %v", err)
	}

	created, err := repo.GetByEmail(ctx, params.Email)
	if err != nil {
		t.Fatalf("expected the admin account to exist after seeding: %v", err)
	}
	roleNames, err := rolesService.ListNamesForUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roleNames) != 1 || roleNames[0] != "admin" {
		t.Errorf("expected roles [admin], got %v", roleNames)
	}

	if err := seedAdmin(ctx, repo, service, params); err != nil {
		t.Fatalf("2nd seedAdmin call: expected a silent no-op, got error: %v", err)
	}
}

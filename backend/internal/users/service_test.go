package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/licenses"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/users"
)

func newUsersService(t *testing.T) (*users.Service, *roles.Service) {
	svc, roleSvc, _, _, _ := newUsersServiceWithMemberships(t)
	return svc, roleSvc
}

// newUsersServiceWithMemberships also returns the memberships.Service and
// licenses.Service built on the same transaction, for tests that need to
// start a membership period or issue a license and see it reflected
// through users.Service.
func newUsersServiceWithMemberships(t *testing.T) (*users.Service, *roles.Service, *memberships.Service, *licenses.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	svc := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(svc)
	return svc, rolesService, membershipsService, licensesService, tx
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
		FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t), RoleNames: []string{"material"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(u.Roles) != 1 || u.Roles[0] != "material" {
		t.Errorf("expected role [material], got %v", u.Roles)
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

func TestServiceReportsLatestMembershipDates(t *testing.T) {
	svc, _, membershipsService, _, _ := newUsersServiceWithMemberships(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.MembershipStartDate != nil || u.MembershipEndDate != nil {
		t.Errorf("expected a brand-new person to have no membership dates, got start=%v end=%v", u.MembershipStartDate, u.MembershipEndDate)
	}

	startDate, err := time.Parse("2006-01-02", "2020-01-01")
	if err != nil {
		t.Fatalf("parse start date: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: u.ID, StartDate: startDate}); err != nil {
		t.Fatalf("start membership: %v", err)
	}

	current, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.MembershipStartDate == nil || !current.MembershipStartDate.Equal(startDate) {
		t.Errorf("expected membership_start_date %v, got %v", startDate, current.MembershipStartDate)
	}
	if current.MembershipEndDate != nil {
		t.Errorf("expected a still-active membership to have no end date, got %v", current.MembershipEndDate)
	}
}

// Someone who left the club and rejoined has two membership periods.
// Their reported alta must be the first period's start — when they
// originally joined — not the second period's, even though the second
// period is the "latest" one and does supply the baja/still-active
// status.
func TestServiceReportsFirstAltaAcrossMultipleMembershipPeriods(t *testing.T) {
	svc, _, membershipsService, _, _ := newUsersServiceWithMemberships(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	firstStart, _ := time.Parse("2006-01-02", "2015-01-01")
	firstEnd, _ := time.Parse("2006-01-02", "2016-01-01")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: u.ID, StartDate: firstStart}); err != nil {
		t.Fatalf("create first period: %v", err)
	}
	firstPeriods, err := membershipsService.ListByUser(ctx, u.ID)
	if err != nil || len(firstPeriods) == 0 {
		t.Fatalf("list by user: %v", err)
	}
	if _, err := membershipsService.Update(ctx, firstPeriods[0].ID, memberships.UpdateInput{StartDate: firstStart, EndDate: &firstEnd}); err != nil {
		t.Fatalf("end first period: %v", err)
	}

	secondStart, _ := time.Parse("2006-01-02", "2020-01-01")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: u.ID, StartDate: secondStart}); err != nil {
		t.Fatalf("create second period: %v", err)
	}

	current, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.MembershipStartDate == nil || !current.MembershipStartDate.Equal(firstStart) {
		t.Errorf("expected membership_start_date %v (the first alta), got %v", firstStart, current.MembershipStartDate)
	}
	if current.MembershipEndDate != nil {
		t.Errorf("expected the currently-open second period to report no end date, got %v", current.MembershipEndDate)
	}
}

func TestServiceReportsOnLeave(t *testing.T) {
	svc, _, membershipsService, _, _ := newUsersServiceWithMemberships(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	startDate, err := time.Parse("2006-01-02", "2020-01-01")
	if err != nil {
		t.Fatalf("parse start date: %v", err)
	}
	m, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: u.ID, StartDate: startDate})
	if err != nil {
		t.Fatalf("start membership: %v", err)
	}

	active, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if active.OnLeave {
		t.Error("expected an active member with no leave to report on_leave=false")
	}

	yesterday, err := time.Parse("2006-01-02", time.Now().AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		t.Fatalf("parse yesterday: %v", err)
	}
	tomorrow, err := time.Parse("2006-01-02", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		t.Fatalf("parse tomorrow: %v", err)
	}
	if _, err := membershipsService.CreateLeave(ctx, memberships.CreateLeaveInput{
		MembershipID: m.ID, StartDate: yesterday, EndDate: tomorrow,
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}

	onLeave, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !onLeave.OnLeave {
		t.Error("expected a member currently within a leave window to report on_leave=true")
	}
}

func TestServiceAssignsMembershipNumberOnFirstAltaAndKeepsItOnRejoin(t *testing.T) {
	svc, _, membershipsService, _, _ := newUsersServiceWithMemberships(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "One", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.MembershipNumber != nil {
		t.Errorf("expected a brand-new person with no alta to have no membership number, got %v", *first.MembershipNumber)
	}

	startDate, err := time.Parse("2006-01-02", "2020-01-01")
	if err != nil {
		t.Fatalf("parse start date: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: first.ID, StartDate: startDate}); err != nil {
		t.Fatalf("start membership: %v", err)
	}
	withNumber, err := svc.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if withNumber.MembershipNumber == nil {
		t.Fatal("expected a membership number to be assigned on first alta")
	}
	firstNumber := *withNumber.MembershipNumber

	// A second person's first alta gets the next number.
	second, err := svc.Create(ctx, users.CreateInput{FirstName: "B", LastName: "Two", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	secondStart, err := time.Parse("2006-01-02", "2020-02-01")
	if err != nil {
		t.Fatalf("parse second start date: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: second.ID, StartDate: secondStart}); err != nil {
		t.Fatalf("start second membership: %v", err)
	}
	secondWithNumber, err := svc.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if secondWithNumber.MembershipNumber == nil || *secondWithNumber.MembershipNumber != firstNumber+1 {
		t.Errorf("expected membership number %d, got %v", firstNumber+1, secondWithNumber.MembershipNumber)
	}

	// Ending the first person's period and rejoining later must keep
	// their original number, not assign a new one.
	endDate, err := time.Parse("2006-01-02", "2020-06-01")
	if err != nil {
		t.Fatalf("parse end date: %v", err)
	}
	periods, err := membershipsService.ListByUser(ctx, first.ID)
	if err != nil || len(periods) == 0 {
		t.Fatalf("list by user: %v", err)
	}
	latest := periods[0]
	if _, err := membershipsService.Update(ctx, latest.ID, memberships.UpdateInput{StartDate: latest.StartDate, EndDate: &endDate}); err != nil {
		t.Fatalf("end membership: %v", err)
	}
	rejoinDate, err := time.Parse("2006-01-02", "2021-01-01")
	if err != nil {
		t.Fatalf("parse rejoin date: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: first.ID, StartDate: rejoinDate}); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	rejoined, err := svc.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("get rejoined: %v", err)
	}
	if rejoined.MembershipNumber == nil || *rejoined.MembershipNumber != firstNumber {
		t.Errorf("expected rejoining to keep membership number %d, got %v", firstNumber, rejoined.MembershipNumber)
	}
	// The reported alta is the very first one, not this rejoin.
	if rejoined.MembershipStartDate == nil || !rejoined.MembershipStartDate.Equal(startDate) {
		t.Errorf("expected membership_start_date to stay %v (the original alta), got %v", startDate, rejoined.MembershipStartDate)
	}
	if rejoined.MembershipEndDate != nil {
		t.Errorf("expected the rejoined (current) period to report no end date, got %v", rejoined.MembershipEndDate)
	}
}

func TestServiceReportsLatestLicense(t *testing.T) {
	svc, _, membershipsService, licensesService, tx := newUsersServiceWithMemberships(t)
	ctx := context.Background()

	u, err := svc.Create(ctx, users.CreateInput{FirstName: "A", LastName: "B", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.LicenseType != nil {
		t.Errorf("expected a brand-new person to have no license, got type %v", u.LicenseType)
	}

	startDate, err := time.Parse("2006-01-02", "2020-01-01")
	if err != nil {
		t.Fatalf("parse start date: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: u.ID, StartDate: startDate}); err != nil {
		t.Fatalf("start membership: %v", err)
	}
	issuedDate, err := time.Parse("2006-01-02", "2026-03-01")
	if err != nil {
		t.Fatalf("parse issued date: %v", err)
	}
	licenseTypeID := testutil.LicenseTypeID(t, tx, "B1 PLUS")
	if _, err := licensesService.Create(ctx, licenses.CreateInput{UserID: u.ID, LicenseTypeID: licenseTypeID, IssuedDate: issuedDate}); err != nil {
		t.Fatalf("create license: %v", err)
	}

	current, err := svc.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if current.LicenseType == nil || *current.LicenseType != "B1 PLUS" {
		t.Errorf("expected license_type %q, got %v", "B1 PLUS", current.LicenseType)
	}
	if current.LicenseIssuedDate == nil || !current.LicenseIssuedDate.Equal(issuedDate) {
		t.Errorf("expected license_issued_date %v, got %v", issuedDate, current.LicenseIssuedDate)
	}
	wantValidUntil, _ := time.Parse("2006-01-02", "2026-12-31")
	if current.LicenseValidUntil == nil || !current.LicenseValidUntil.Equal(wantValidUntil) {
		t.Errorf("expected license_valid_until %v, got %v", wantValidUntil, current.LicenseValidUntil)
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

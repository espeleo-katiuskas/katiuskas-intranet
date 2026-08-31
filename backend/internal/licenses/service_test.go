package licenses_test

import (
	"context"
	"errors"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func TestServiceCreateRejectsSomeoneWhoWasNeverAMember(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	_, err := svc.Create(context.Background(), licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"),
	})
	if !errors.Is(err, licenses.ErrNotActiveMember) {
		t.Errorf("expected ErrNotActiveMember, got %v", err)
	}
}

func TestServiceCreateRejectsSomeoneWhoHasLeft(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	m, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	pastEnd := date("2024-01-01")
	if _, err := membershipsService.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end membership: %v", err)
	}

	_, err = svc.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"),
	})
	if !errors.Is(err, licenses.ErrNotActiveMember) {
		t.Errorf("expected ErrNotActiveMember, got %v", err)
	}
}

func TestServiceCreateAllowsACurrentlyActiveMember(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	l, err := svc.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"),
	})
	if err != nil {
		t.Fatalf("expected the license to be created, got %v", err)
	}
	if l.UserID != person.ID {
		t.Errorf("expected user id %d, got %d", person.ID, l.UserID)
	}
	if l.Type != "A BASICA" {
		t.Errorf("expected type %q, got %q", "A BASICA", l.Type)
	}
}

func TestServiceCreateRejectsUnknownLicenseType(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: 999999, IssuedDate: date("2026-01-01")})
	if !errors.Is(err, licenses.ErrTypeNotFound) {
		t.Errorf("expected ErrTypeNotFound, got %v", err)
	}
}

func TestServiceCreateRequiresOtherDetailForTheOtraType(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	otraID := testutil.LicenseTypeID(t, tx, "Otra")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: otraID, IssuedDate: date("2026-01-01")})
	if !errors.Is(err, licenses.ErrOtherDetailRequired) {
		t.Errorf("expected ErrOtherDetailRequired, got %v", err)
	}

	detail := "FME"
	l, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: otraID, OtherDetail: &detail, IssuedDate: date("2026-01-01")})
	if err != nil {
		t.Fatalf("expected the license to be created with other_detail, got %v", err)
	}
	if l.Type != "Otra: FME" {
		t.Errorf(`expected type "Otra: FME", got %q`, l.Type)
	}
}

func TestServiceCreateRejectsOtherDetailForANonOtraType(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	detail := "unwanted"
	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeID, OtherDetail: &detail, IssuedDate: date("2026-01-01")})
	if !errors.Is(err, licenses.ErrOtherDetailNotAllowed) {
		t.Errorf("expected ErrOtherDetailNotAllowed, got %v", err)
	}
}

func TestServiceUpdateEnforcesTheSameOtherDetailRuleAsCreate(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	otraID := testutil.LicenseTypeID(t, tx, "Otra")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: otraID, IssuedDate: date("2026-01-01")}); !errors.Is(err, licenses.ErrOtherDetailRequired) {
		t.Errorf("expected ErrOtherDetailRequired, got %v", err)
	}

	detail := "FME"
	updated, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: otraID, OtherDetail: &detail, IssuedDate: date("2026-01-01")})
	if err != nil {
		t.Fatalf("expected the update to succeed with other_detail, got %v", err)
	}
	if updated.Type != "Otra: FME" {
		t.Errorf(`expected type "Otra: FME", got %q`, updated.Type)
	}
}

func TestServiceUpdateSucceedsEvenAfterMembershipEnded(t *testing.T) {
	// Unlike Create, Update doesn't require the person to currently be an
	// active member — it's correcting an existing license, not assigning
	// a new one.
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	typeB1 := testutil.LicenseTypeID(t, tx, "B1 PLUS")
	ctx := context.Background()

	m, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2020-06-01")})
	if err != nil {
		t.Fatalf("create license: %v", err)
	}
	pastEnd := date("2021-01-01")
	if _, err := membershipsService.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end membership: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: typeB1, IssuedDate: date("2020-06-01")}); err != nil {
		t.Errorf("expected the correction to succeed for a former member, got %v", err)
	}
}

func TestServiceDelete(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	latest, err := svc.LatestByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("latest by user: %v", err)
	}
	if latest != nil {
		t.Errorf("expected no license left after delete, got %+v", latest)
	}
}

func TestServiceListTypesIncludesTheFullCatalog(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := licenses.NewService(licenses.NewRepository(tx), memberships.NewService(memberships.NewRepository(tx)))

	list, err := svc.ListTypes(context.Background())
	if err != nil {
		t.Fatalf("list types: %v", err)
	}
	names := map[string]bool{}
	var otraIsOther bool
	for _, lt := range list {
		names[lt.Name] = true
		if lt.Name == "Otra" {
			otraIsOther = lt.IsOther
		}
	}
	for _, want := range []string{
		"A BASICA", "A BASICA Infantil", "A BASICA Juvenil",
		"B PLUS", "B1 PLUS Juvenil",
		"ZERO", "ASEDEB", "FAE (Andaluza)", "FERM (Murciana)", "FMM (Montaña)", "Otra",
	} {
		if !names[want] {
			t.Errorf("expected the catalog to include %q, got %+v", want, list)
		}
	}
	if !otraIsOther {
		t.Error(`expected "Otra" to have is_other = true`)
	}
}

func TestServiceLatestByUserReturnsNilWhenNeverLicensed(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := licenses.NewService(licenses.NewRepository(tx), memberships.NewService(memberships.NewRepository(tx)))
	person := testutil.InsertUser(t, tx, "")

	latest, err := svc.LatestByUser(context.Background(), person.ID)
	if err != nil {
		t.Fatalf("latest by user: %v", err)
	}
	if latest != nil {
		t.Errorf("expected nil, got %+v", latest)
	}
}

func TestServiceLatestByUserReturnsTheMostRecentlyIssued(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	typeB := testutil.LicenseTypeID(t, tx, "B PLUS")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2025-01-01")}); err != nil {
		t.Fatalf("create 2025 license: %v", err)
	}
	current, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeB, IssuedDate: date("2026-01-01")})
	if err != nil {
		t.Fatalf("create 2026 license: %v", err)
	}

	latest, err := svc.LatestByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("latest by user: %v", err)
	}
	if latest == nil || latest.ID != current.ID {
		t.Fatalf("expected the 2026 license, got %+v", latest)
	}
	if !latest.ValidUntil.Equal(date("2026-12-31")) {
		t.Errorf("expected valid_until 2026-12-31, got %v", latest.ValidUntil)
	}
}

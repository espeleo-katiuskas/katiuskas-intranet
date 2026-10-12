package licenses_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
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
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if !errors.Is(err, licenses.ErrNotActiveMember) {
		t.Errorf("expected ErrNotActiveMember, got %v", err)
	}
}

// TestServiceCreateAllowsALicenseDatedDuringAPastMembershipPeriod covers
// the case Create must not reject: someone who was a socio when the
// license was actually issued, even though they've since caused baja
// and aren't a member right now.
func TestServiceCreateAllowsALicenseDatedDuringAPastMembershipPeriod(t *testing.T) {
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

	l, err := svc.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2023-06-01"), Year: 2023, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("expected the license to be created despite the person no longer being a member, got %v", err)
	}
	if l.UserID != person.ID {
		t.Errorf("expected user id %d, got %d", person.ID, l.UserID)
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
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
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

	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: 999999, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
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

	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: otraID, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if !errors.Is(err, licenses.ErrOtherDetailRequired) {
		t.Errorf("expected ErrOtherDetailRequired, got %v", err)
	}

	detail := "FME"
	l, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: otraID, OtherDetail: &detail, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
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
	_, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeID, OtherDetail: &detail, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
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
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: otraID, IssuedDate: date("2026-01-01"), Year: 2026}); !errors.Is(err, licenses.ErrOtherDetailRequired) {
		t.Errorf("expected ErrOtherDetailRequired, got %v", err)
	}

	detail := "FME"
	updated, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: otraID, OtherDetail: &detail, IssuedDate: date("2026-01-01"), Year: 2026})
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
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2020-06-01"), Year: 2020, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create license: %v", err)
	}
	pastEnd := date("2021-01-01")
	if _, err := membershipsService.Update(ctx, m.ID, memberships.UpdateInput{StartDate: m.StartDate, EndDate: &pastEnd}); err != nil {
		t.Fatalf("end membership: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, licenses.UpdateInput{LicenseTypeID: typeB1, IssuedDate: date("2020-06-01"), Year: 2020}); err != nil {
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
	created, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: date("2026-01-01"), Year: 2026, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left, err := svc.ListByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("expected no license left after delete, got %+v", left)
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

func TestServiceCurrentByUserReturnsNilWhenNeverLicensed(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := licenses.NewService(licenses.NewRepository(tx), memberships.NewService(memberships.NewRepository(tx)))
	person := testutil.InsertUser(t, tx, "")

	current, err := svc.CurrentByUser(context.Background(), person.ID)
	if err != nil {
		t.Fatalf("current by user: %v", err)
	}
	if current != nil {
		t.Errorf("expected nil, got %+v", current)
	}
}

// This year's license is the current one, not last year's — and it runs
// to December 31st.
func TestServiceCurrentByUserReturnsThisYearsLicense(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeA := testutil.LicenseTypeID(t, tx, "A BASICA")
	typeB := testutil.LicenseTypeID(t, tx, "B PLUS")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2015-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	year := time.Now().Year()
	if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeA, IssuedDate: time.Date(year-1, 1, 1, 0, 0, 0, 0, time.UTC), Year: year - 1, TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
		t.Fatalf("create last year's license: %v", err)
	}
	thisYears, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeB, IssuedDate: time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), Year: year, TransactionID: testutil.InsertTransaction(t, tx)})
	if err != nil {
		t.Fatalf("create this year's license: %v", err)
	}

	current, err := svc.CurrentByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("current by user: %v", err)
	}
	if current == nil || current.ID != thisYears.ID {
		t.Fatalf("expected this year's license, got %+v", current)
	}
	if want := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC); !current.ValidUntil.Equal(want) {
		t.Errorf("expected valid_until %v, got %v", want, current.ValidUntil)
	}
}

// TestIsCurrentlyFederatedFalseWhenNeverLicensed, TestIsCurrentlyFederatedTrueForALicenseIssuedThisYear,
// and TestIsCurrentlyFederatedFalseForAnExpiredLicense cover
// IsCurrentlyFederated directly — until now it was only ever exercised
// indirectly through activities.Service's own federation check.

func TestIsCurrentlyFederatedFalseWhenNeverLicensed(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := licenses.NewService(licenses.NewRepository(tx), memberships.NewService(memberships.NewRepository(tx)))
	person := testutil.InsertUser(t, tx, "")

	federated, err := svc.IsCurrentlyFederated(context.Background(), person.ID)
	if err != nil {
		t.Fatalf("is currently federated: %v", err)
	}
	if federated {
		t.Error("expected someone with no license at all to not be federated")
	}
}

func TestIsCurrentlyFederatedTrueForALicenseIssuedThisYear(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	thisYear := time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeID, IssuedDate: thisYear, Year: thisYear.Year(), TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
		t.Fatalf("create license: %v", err)
	}

	federated, err := svc.IsCurrentlyFederated(ctx, person.ID)
	if err != nil {
		t.Fatalf("is currently federated: %v", err)
	}
	if !federated {
		t.Error("expected a license issued this year to still be currently federated")
	}
}

func TestIsCurrentlyFederatedFalseForAnExpiredLicense(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2000-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	lastYear := time.Date(time.Now().Year()-1, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeID, IssuedDate: lastYear, Year: lastYear.Year(), TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
		t.Fatalf("create license: %v", err)
	}

	federated, err := svc.IsCurrentlyFederated(ctx, person.ID)
	if err != nil {
		t.Fatalf("is currently federated: %v", err)
	}
	if federated {
		t.Error("expected a license issued last year (valid_until December 31st of last year) to no longer be federated")
	}
}

// A license can only be tied to a transaction that touches an account
// marked allows_licenses (migration 00017): one that only has an account
// for dues, or for neither, is rejected.
func TestServiceCreateRejectsATransactionWithoutALicensesAccount(t *testing.T) {
	for _, c := range []struct {
		name                     string
		allowsDues, allowsLicens bool
	}{
		{"an account for dues only", true, false},
		{"no flagged account at all", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tx := testutil.NewTx(t)
			membershipsService := memberships.NewService(memberships.NewRepository(tx))
			svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
			person := testutil.InsertUser(t, tx, "")
			typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
			ctx := context.Background()
			if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
				t.Fatalf("create membership: %v", err)
			}

			_, err := svc.Create(ctx, licenses.CreateInput{
				UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026,
				TransactionID: testutil.InsertTransactionOn(t, tx, c.allowsDues, c.allowsLicens)})
			if !errors.Is(err, licenses.ErrTransactionNotForLicenses) {
				t.Errorf("expected ErrTransactionNotForLicenses, got %v", err)
			}
		})
	}
}

func TestServiceCreateAcceptsATransactionOnALicensesAccountThatIsNotAlsoForDues(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	if _, err := svc.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: typeID, IssuedDate: date("2026-01-01"), Year: 2026,
		TransactionID: testutil.InsertTransactionOn(t, tx, false, true)}); err != nil {
		t.Errorf("expected a licenses-only account to be enough, got %v", err)
	}
}

// A license bought in December for next year is the latest issued, but
// the one covering today is still this year's.
func TestCurrentByUserIgnoresALaterLicenseForNextYear(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2015-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	year := time.Now().Year()
	for _, l := range []struct {
		issued time.Time
		year   int
	}{{time.Date(year, 1, 10, 0, 0, 0, 0, time.UTC), year}, {time.Date(year, 1, 11, 0, 0, 0, 0, time.UTC), year + 1}} {
		if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, LicenseTypeID: typeID, IssuedDate: l.issued, Year: l.year, TransactionID: testutil.InsertTransaction(t, tx)}); err != nil {
			t.Fatalf("create license: %v", err)
		}
	}

	current, err := svc.CurrentByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("current by user: %v", err)
	}
	if current == nil || current.Year != year {
		t.Errorf("expected this year's license, got %+v", current)
	}
	if federated, _ := svc.IsCurrentlyFederated(ctx, person.ID); !federated {
		t.Error("expected to be federated through this year's license")
	}
}

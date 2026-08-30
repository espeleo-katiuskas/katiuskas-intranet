package licenses_test

import (
	"context"
	"errors"
	"testing"

	"katiuskas-intranet/backend/internal/licenses"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/testutil"
)

func TestServiceCreateRejectsSomeoneWhoWasNeverAMember(t *testing.T) {
	tx := testutil.NewTx(t)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	svc := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	person := testutil.InsertUser(t, tx, "")

	_, err := svc.Create(context.Background(), licenses.CreateInput{
		UserID: person.ID, Type: "A BASICA", IssuedDate: date("2026-01-01"),
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
		UserID: person.ID, Type: "A BASICA", IssuedDate: date("2026-01-01"),
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
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	l, err := svc.Create(ctx, licenses.CreateInput{
		UserID: person.ID, Type: "A BASICA", IssuedDate: date("2026-01-01"),
	})
	if err != nil {
		t.Fatalf("expected the license to be created, got %v", err)
	}
	if l.UserID != person.ID {
		t.Errorf("expected user id %d, got %d", person.ID, l.UserID)
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
	ctx := context.Background()

	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: person.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, Type: "A BASICA", IssuedDate: date("2025-01-01")}); err != nil {
		t.Fatalf("create 2025 license: %v", err)
	}
	current, err := svc.Create(ctx, licenses.CreateInput{UserID: person.ID, Type: "B PLUS", IssuedDate: date("2026-01-01")})
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

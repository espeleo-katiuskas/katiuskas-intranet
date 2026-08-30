package licenses_test

import (
	"context"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/licenses"
	"katiuskas-intranet/backend/internal/testutil"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCreateComputesValidUntilAsDecember31(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	l, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, Zone: "A", Modality: "PLUS", IssuedDate: date("2026-03-15"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, l.UserID)
	}
	if l.Zone != "A" || l.Modality != "PLUS" {
		t.Errorf("expected zone A / modality PLUS, got %s / %s", l.Zone, l.Modality)
	}
	if !l.ValidUntil.Equal(date("2026-12-31")) {
		t.Errorf("expected valid_until 2026-12-31, got %v", l.ValidUntil)
	}
}

func TestCreateAcceptsArbitraryZoneAndModality(t *testing.T) {
	// Zone/modality are free text on purpose (see the migration comment):
	// the club expects values beyond A/B/B1 and BASICA/PLUS to show up
	// over time, so nothing here should reject an unfamiliar one.
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	l, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, Zone: "C", Modality: "ELITE", IssuedDate: date("2026-01-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.Zone != "C" || l.Modality != "ELITE" {
		t.Errorf("expected zone C / modality ELITE, got %s / %s", l.Zone, l.Modality)
	}
}

func TestListByUserOrdersNewestFirstAndScopesToTheUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.Create(ctx, licenses.CreateInput{UserID: other.ID, Zone: "A", Modality: "BASICA", IssuedDate: date("2026-01-01")}); err != nil {
		t.Fatalf("create for other: %v", err)
	}
	older, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, Zone: "A", Modality: "BASICA", IssuedDate: date("2025-02-01")})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, Zone: "B", Modality: "PLUS", IssuedDate: date("2026-02-01")})
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}

	list, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 licenses for this user, got %d", len(list))
	}
	if list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("expected newest-first order [%d, %d], got [%d, %d]", newer.ID, older.ID, list[0].ID, list[1].ID)
	}
}

func TestListByUserReturnsEmptySliceNotNil(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	list, err := repo.ListByUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if list == nil {
		t.Error("expected an empty slice, got nil (would serialize to JSON null)")
	}
}

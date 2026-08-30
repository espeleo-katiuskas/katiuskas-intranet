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
		UserID: user.ID, Type: "A PLUS", IssuedDate: date("2026-03-15"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.UserID != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, l.UserID)
	}
	if l.Type != "A PLUS" {
		t.Errorf("expected type %q, got %q", "A PLUS", l.Type)
	}
	if !l.ValidUntil.Equal(date("2026-12-31")) {
		t.Errorf("expected valid_until 2026-12-31, got %v", l.ValidUntil)
	}
}

func TestCreateAcceptsArbitraryType(t *testing.T) {
	// type is free text on purpose (see the migration comment): real
	// license types don't decompose into a zone plus a modality ("FME",
	// "Murciana", "A BASICA Infantil"), so nothing here should reject an
	// unfamiliar one.
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")

	l, err := repo.Create(context.Background(), licenses.CreateInput{
		UserID: user.ID, Type: "FME", IssuedDate: date("2026-01-01"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if l.Type != "FME" {
		t.Errorf("expected type %q, got %q", "FME", l.Type)
	}
}

func TestListByUserOrdersNewestFirstAndScopesToTheUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := licenses.NewRepository(tx)
	user := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	ctx := context.Background()

	if _, err := repo.Create(ctx, licenses.CreateInput{UserID: other.ID, Type: "A BASICA", IssuedDate: date("2026-01-01")}); err != nil {
		t.Fatalf("create for other: %v", err)
	}
	older, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, Type: "A BASICA", IssuedDate: date("2025-02-01")})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer, err := repo.Create(ctx, licenses.CreateInput{UserID: user.ID, Type: "B1 PLUS", IssuedDate: date("2026-02-01")})
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

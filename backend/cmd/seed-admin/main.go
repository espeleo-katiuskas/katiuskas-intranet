// Command seed-admin creates the first administrator.
//
// It exists because POST /api/v1/users already requires an admin role
// to call it: without this command there would be no way into a
// freshly migrated database at all. Safe to run more than once — if the
// email already has a person record, it does nothing.
//
// There's no password to report: sign-in is by magic link. Request one
// from the login page with SEED_ADMIN_EMAIL and SEED_ADMIN_PHONE, then
// check the API's logs for the link (or your SMTP inbox, if SMTP_* is
// configured).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	repo := users.NewRepository(pool)
	rolesService := roles.NewService(roles.NewRepository(pool))
	membershipsService := memberships.NewService(memberships.NewRepository(pool))
	licensesService := licenses.NewService(licenses.NewRepository(pool), membershipsService)
	service := users.NewService(repo, rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(service)

	email := getEnv("SEED_ADMIN_EMAIL", "admin@club.test")
	phone := getEnv("SEED_ADMIN_PHONE", "600000000")
	firstName := getEnv("SEED_ADMIN_FIRST_NAME", "Admin")
	lastName := getEnv("SEED_ADMIN_LAST_NAME", "Club")
	birthDateRaw := getEnv("SEED_ADMIN_BIRTH_DATE", "1970-01-01")
	birthDate, err := time.Parse("2006-01-02", birthDateRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SEED_ADMIN_BIRTH_DATE must be YYYY-MM-DD: %v\n", err)
		os.Exit(1)
	}

	if _, err := repo.GetByEmail(ctx, email); err == nil {
		slog.Info("admin account already exists, nothing to do", "email", email)
		return
	} else if !errors.Is(err, users.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "check for an existing account: %v\n", err)
		os.Exit(1)
	}

	_, err = service.Create(ctx, users.CreateInput{
		FirstName: firstName,
		LastName:  lastName,
		BirthDate: birthDate,
		Email:     &email,
		Phone:     &phone,
		RoleNames: []string{"admin"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create admin account: %v\n", err)
		os.Exit(1)
	}

	slog.Info("admin account created", "email", email, "phone", phone)
	slog.Info("to sign in: request a magic link from the login page with this email and phone, " +
		"then check the API's logs (or your SMTP inbox, if configured) for the link")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

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
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/logging"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func main() {
	slog.SetDefault(logging.New())
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

	birthDateRaw := getEnv("SEED_ADMIN_BIRTH_DATE", "1970-01-01")
	birthDate, err := time.Parse("2006-01-02", birthDateRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SEED_ADMIN_BIRTH_DATE must be YYYY-MM-DD: %v\n", err)
		os.Exit(1)
	}

	err = seedAdmin(ctx, repo, service, seedParams{
		Email:     getEnv("SEED_ADMIN_EMAIL", "admin@club.test"),
		Phone:     getEnv("SEED_ADMIN_PHONE", "600000000"),
		FirstName: getEnv("SEED_ADMIN_FIRST_NAME", "Admin"),
		LastName:  getEnv("SEED_ADMIN_LAST_NAME", "Club"),
		BirthDate: birthDate,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// seedParams is the first administrator's own data, gathered from
// SEED_ADMIN_* env vars (or their defaults) by main() before calling
// seedAdmin.
type seedParams struct {
	Email     string
	Phone     string
	FirstName string
	LastName  string
	BirthDate time.Time
}

// seedAdmin creates the first administrator if nobody with this email
// exists yet — idempotent, so running this command again (e.g. as part
// of every deploy) is always safe. Pulled out of main() so a test can
// exercise the actual create-vs-already-exists logic against a real
// (migrated) database directly, without main()'s own env var parsing
// or os.Exit calls getting in the way.
func seedAdmin(ctx context.Context, repo *users.Repository, service *users.Service, p seedParams) error {
	if _, err := repo.GetByEmail(ctx, p.Email); err == nil {
		slog.Info("admin account already exists, nothing to do", "email", p.Email)
		return nil
	} else if !errors.Is(err, users.ErrNotFound) {
		return fmt.Errorf("check for an existing account: %w", err)
	}

	_, err := service.Create(ctx, users.CreateInput{
		FirstName: p.FirstName,
		LastName:  p.LastName,
		BirthDate: p.BirthDate,
		Email:     &p.Email,
		Phone:     &p.Phone,
		RoleNames: []string{"admin"},
	})
	if err != nil {
		return fmt.Errorf("create admin account: %w", err)
	}

	slog.Info("admin account created", "email", p.Email, "phone", p.Phone)
	slog.Info("to sign in: request a magic link from the login page with this email and phone, " +
		"then check the API's logs (or your SMTP inbox, if configured) for the link")
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

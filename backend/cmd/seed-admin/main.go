// Command seed-admin creates the first administrator account.
//
// It exists because POST /api/v1/users already requires an admin or board
// account to call it: without this command there would be no way into a
// freshly migrated database at all. Safe to run more than once — if the
// username already has an account, it does nothing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"katiuskas-intranet/backend/internal/platform/db"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/users"
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
	service := users.NewService(repo, rolesService)

	username := getEnv("SEED_ADMIN_USERNAME", "admin")
	email := getEnv("SEED_ADMIN_EMAIL", "admin@club.test")
	firstName := getEnv("SEED_ADMIN_FIRST_NAME", "Admin")
	lastName := getEnv("SEED_ADMIN_LAST_NAME", "Club")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	usingDefaultPassword := password == ""
	if usingDefaultPassword {
		password = "changeme123"
	}

	if _, err := repo.GetCredentialsByUsername(ctx, username); err == nil {
		slog.Info("admin account already exists, nothing to do", "username", username)
		return
	} else if !errors.Is(err, users.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "check for an existing account: %v\n", err)
		os.Exit(1)
	}

	_, err = service.Create(ctx, users.CreateInput{
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
		Username:  username,
		Password:  password,
		RoleNames: []string{"admin"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create admin account: %v\n", err)
		os.Exit(1)
	}

	slog.Info("admin account created", "username", username, "email", email)
	if usingDefaultPassword {
		slog.Warn("created with the default seed password, only fit for local development", "password", password)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

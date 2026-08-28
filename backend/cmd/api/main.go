// Command api starts the Intranet REST API server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"katiuskas-intranet/backend/internal/activities"
	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/authapi"
	"katiuskas-intranet/backend/internal/config"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/inventory"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/platform/db"
	"katiuskas-intranet/backend/internal/users"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	authenticator := auth.NewAuthenticator(cfg.JWTSecret, cfg.AccessTokenTTL)

	usersRepo := users.NewRepository(pool)
	usersService := users.NewService(usersRepo)

	membershipsRepo := memberships.NewRepository(pool)
	membershipsService := memberships.NewService(membershipsRepo)

	activitiesRepo := activities.NewRepository(pool)
	activitiesService := activities.NewService(activitiesRepo)

	inventoryRepo := inventory.NewRepository(pool)
	inventoryService := inventory.NewService(inventoryRepo)

	router := httpapi.NewRouter(authenticator, httpapi.Routes{
		Auth:        authapi.NewHandler(usersService, authenticator, cfg.RefreshTokenTTL),
		Users:       users.NewHandler(usersService),
		Memberships: memberships.NewHandler(membershipsService),
		Activities:  activities.NewHandler(activitiesService),
		Inventory:   inventory.NewHandler(inventoryService),
	})

	slog.Info("starting server", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

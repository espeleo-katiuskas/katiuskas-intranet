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
	"katiuskas-intranet/backend/internal/licenses"
	"katiuskas-intranet/backend/internal/mail"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/platform/db"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/tokens"
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

	rolesRepo := roles.NewRepository(pool)
	rolesService := roles.NewService(rolesRepo)

	membershipsRepo := memberships.NewRepository(pool)
	membershipsService := memberships.NewService(membershipsRepo)

	licensesRepo := licenses.NewRepository(pool)
	licensesService := licenses.NewService(licensesRepo, membershipsService)

	usersRepo := users.NewRepository(pool)
	usersService := users.NewService(usersRepo, rolesService, membershipsService, licensesService)

	tokensRepo := tokens.NewRepository(pool)
	tokensService := tokens.NewService(tokensRepo, rolesService)

	authenticator := auth.NewAuthenticator(tokensService)

	activitiesRepo := activities.NewRepository(pool)
	activitiesService := activities.NewService(activitiesRepo)

	inventoryRepo := inventory.NewRepository(pool)
	inventoryService := inventory.NewService(inventoryRepo)

	mailer := mail.NewSenderFromConfig(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)

	router := httpapi.NewRouter(httpapi.Routes{
		Auth: authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, authapi.Options{
			TokenTTL:          cfg.TokenTTL,
			MagicLinkTTL:      cfg.MagicLinkTTL,
			MagicLinkCooldown: cfg.MagicLinkCooldown,
			PublicBaseURL:     cfg.PublicBaseURL,
		}),
		Users:       users.NewHandler(usersService, authenticator),
		Roles:       roles.NewHandler(rolesService, authenticator),
		Tokens:      tokens.NewHandler(tokensService, authenticator),
		Memberships: memberships.NewHandler(membershipsService, authenticator),
		Licenses:    licenses.NewHandler(licensesService, authenticator),
		Activities:  activities.NewHandler(activitiesService, authenticator),
		Inventory:   inventory.NewHandler(inventoryService, authenticator),
	})

	slog.Info("starting server", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

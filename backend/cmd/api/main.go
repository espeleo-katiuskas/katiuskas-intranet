// Command api starts the Intranet REST API server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/authapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/board"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/config"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/inventory"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/library"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/mail"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
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

	boardRepo := board.NewRepository(pool)
	boardService := board.NewService(boardRepo)

	licensesRepo := licenses.NewRepository(pool)
	licensesService := licenses.NewService(licensesRepo, membershipsService)

	usersRepo := users.NewRepository(pool)
	usersService := users.NewService(usersRepo, rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(usersService)

	tokensRepo := tokens.NewRepository(pool)
	tokensService := tokens.NewService(tokensRepo, rolesService)

	authenticator := auth.NewAuthenticator(tokensService)

	activitiesRepo := activities.NewRepository(pool)
	activitiesService := activities.NewService(activitiesRepo, licensesService)

	inventoryRepo := inventory.NewRepository(pool)
	inventoryService := inventory.NewService(inventoryRepo)

	libraryService, err := library.NewService(cfg.LibraryRoot)
	if err != nil {
		slog.Error("set up document library", "error", err, "root", cfg.LibraryRoot)
		os.Exit(1)
	}

	mailer := mail.NewSenderFromConfig(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)

	router := httpapi.NewRouter(httpapi.Routes{
		Auth: authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, authapi.Options{
			TokenTTL:          cfg.TokenTTL,
			MagicLinkTTL:      cfg.MagicLinkTTL,
			MagicLinkCooldown: cfg.MagicLinkCooldown,
			PublicBaseURL:     cfg.PublicBaseURL,
		}),
		Users:       users.NewHandler(usersService, authenticator),
		Roles:       roles.NewHandler(rolesService, authenticator, usersService),
		Tokens:      tokens.NewHandler(tokensService, authenticator, usersService, cfg.ImpersonationTokenTTL),
		Memberships: memberships.NewHandler(membershipsService, authenticator, usersService),
		Licenses:    licenses.NewHandler(licensesService, authenticator, usersService),
		Activities:  activities.NewHandler(activitiesService, authenticator, usersService),
		Inventory:   inventory.NewHandler(inventoryService, authenticator, usersService),
		Board:       board.NewHandler(boardService, authenticator, usersService),
		Library:     library.NewHandler(libraryService, authenticator),
	})

	slog.Info("starting server", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

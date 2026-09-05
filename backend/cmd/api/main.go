// Command api starts the Club de Espeleología Katiuskas intranet REST API
// server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
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
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegrambot"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/userfiles"
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

	// Rebuilt now that cfg is available, so every subsequent slog call
	// (including ones below) also gets forwarded to Telegram on error —
	// only "load config" above, which can't reach cfg by definition,
	// misses out.
	telegramSender := telegram.NewSenderFromConfig(cfg.TelegramBotToken, cfg.TelegramLogChatID)
	logger = slog.New(telegram.NewLogHandler(logger.Handler(), telegramSender, slog.LevelError))
	slog.SetDefault(logger)

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Only wrapped when Telegram is actually configured — otherwise every
	// single database write in the app would fall through to
	// NoopSender's own "not sent" info log, which is fine once per
	// login or error but far too noisy per statement.
	var conn db.PGX = pool
	if cfg.TelegramBotToken != "" && cfg.TelegramLogChatID != "" {
		conn = db.NewNotifyingPGX(pool, func(ctx context.Context, operation, table string) {
			text := fmt.Sprintf("[DB] %s %s", strings.ToUpper(operation), table)
			if err := telegramSender.Send(ctx, text); err != nil {
				slog.Error("notify db mutation to telegram", "error", err)
			}
		})
	}

	rolesRepo := roles.NewRepository(conn)
	rolesService := roles.NewService(rolesRepo)

	membershipsRepo := memberships.NewRepository(conn)
	membershipsService := memberships.NewService(membershipsRepo)

	boardRepo := board.NewRepository(conn)
	boardService := board.NewService(boardRepo)

	licensesRepo := licenses.NewRepository(conn)
	licensesService := licenses.NewService(licensesRepo, membershipsService)

	usersRepo := users.NewRepository(conn)
	usersService := users.NewService(usersRepo, rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(usersService)

	tokensRepo := tokens.NewRepository(conn)
	tokensService := tokens.NewService(tokensRepo, rolesService)

	authenticator := auth.NewAuthenticator(tokensService)

	activitiesRepo := activities.NewRepository(conn)
	activitiesService := activities.NewService(activitiesRepo, licensesService)

	inventoryRepo := inventory.NewRepository(conn)
	inventoryService := inventory.NewService(inventoryRepo)

	libraryService, err := library.NewService(cfg.LibraryRoot)
	if err != nil {
		slog.Error("set up document library", "error", err, "root", cfg.LibraryRoot)
		os.Exit(1)
	}

	userFilesService, err := userfiles.NewService(cfg.UserFilesRoot)
	if err != nil {
		slog.Error("set up user files", "error", err, "root", cfg.UserFilesRoot)
		os.Exit(1)
	}

	activityResourcesStorage, err := activityresources.NewStorage(cfg.ActivityResourcesRoot)
	if err != nil {
		slog.Error("set up activity resources storage", "error", err, "root", cfg.ActivityResourcesRoot)
		os.Exit(1)
	}
	activityResourcesRepo := activityresources.NewRepository(conn)
	activityResourcesService := activityresources.NewService(activityResourcesRepo, activityResourcesStorage)

	mailer := mail.NewSenderFromConfig(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)

	router := httpapi.NewRouter(httpapi.Routes{
		Auth: authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, telegramSender, authapi.Options{
			TokenTTL:          cfg.TokenTTL,
			MagicLinkTTL:      cfg.MagicLinkTTL,
			MagicLinkCooldown: cfg.MagicLinkCooldown,
			PublicBaseURL:     cfg.PublicBaseURL,
		}),
		Users:             users.NewHandler(usersService, authenticator),
		Roles:             roles.NewHandler(rolesService, authenticator, usersService),
		Tokens:            tokens.NewHandler(tokensService, authenticator, usersService, cfg.ImpersonationTokenTTL),
		Memberships:       memberships.NewHandler(membershipsService, authenticator, usersService),
		Licenses:          licenses.NewHandler(licensesService, authenticator, usersService),
		Activities:        activities.NewHandler(activitiesService, authenticator, usersService),
		Inventory:         inventory.NewHandler(inventoryService, authenticator, usersService),
		Board:             board.NewHandler(boardService, authenticator, usersService),
		Library:           library.NewHandler(libraryService, authenticator),
		UserFiles:         userfiles.NewHandler(userFilesService, authenticator, usersService),
		ActivityResources: activityresources.NewHandler(activityResourcesService, authenticator, activitiesService, usersService),
	})

	// Same gate as the database-mutation notifier above: reacting to
	// Telegram messages is pointless without a bot token, and every
	// linking attempt has to reach the log channel (see
	// telegrambot.Bot.notifyChannel), so both need to be configured.
	if cfg.TelegramBotToken != "" && cfg.TelegramLogChatID != "" {
		bot := telegrambot.New(telegram.NewClient(cfg.TelegramBotToken), usersService, telegramSender)
		go bot.Run(ctx)
	}

	slog.Info("starting server", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

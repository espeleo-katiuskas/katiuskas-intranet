// Command api starts the Club de Espeleología Katiuskas intranet REST API
// server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
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

	telegramConfigured := cfg.TelegramBotToken != "" && cfg.TelegramLogChatID != ""
	// telegramClient is shared between authapi (DMing a magic link to
	// whoever has linked their account) and the bot below — a linked
	// telegram_chat_id can only exist at all because the bot processed a
	// contact share while running, so gating this on the same "fully
	// configured" flag the bot itself uses, rather than just the token,
	// costs nothing in practice.
	var telegramClient authapi.TelegramMessenger
	var botClient *telegram.Client
	if telegramConfigured {
		botClient = telegram.NewClient(cfg.TelegramBotToken)
		telegramClient = botClient
	}

	router, authHandler, usersService, err := buildRouter(cfg, pool, pool, telegramSender, telegramClient)
	if err != nil {
		// buildRouter already logged the specific step that failed.
		os.Exit(1)
	}

	// Reacting to Telegram messages is pointless without a bot token,
	// and every linking attempt has to reach the log channel (see
	// telegrambot.Bot.notifyChannel), so both need to be configured.
	if telegramConfigured {
		bot := telegrambot.New(botClient, usersService, telegramSender, authHandler)
		go bot.Run(ctx)
	}

	// Unconditional, same as the login/error-log notifications — this
	// only fires once per process start (not once per request, unlike
	// mutationNotifier/the bot above), so falling through to
	// NoopSender's own info log when Telegram isn't configured is no
	// noisier than logging it directly would have been anyway.
	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	if err := telegramSender.Send(startupCtx, "🚀 API started"); err != nil {
		slog.Error("notify api startup to telegram", "error", err)
	}
	cancelStartup()

	slog.Info("starting server", "addr", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// pinger is the one method a health check needs — defined locally (the
// same small-interface-per-caller pattern this codebase already uses
// for UserLookup/TelegramMessenger/etc.) since httpapi's own dbPinger
// is unexported. *pgxpool.Pool satisfies this structurally with no
// extra glue.
type pinger interface {
	Ping(ctx context.Context) error
}

// buildRouter wires every domain package's repository/service/handler
// together into the top-level HTTP handler this process serves —
// everything main() used to do inline between opening the pool and
// starting to listen. Pulled out so a test can build the exact same
// composition root against a real (migrated) database and confirm it
// actually assembles and answers sanely, without main()'s own
// process-level concerns (logger setup, os.Exit, starting the Telegram
// bot's long-poll goroutine, the blocking ListenAndServe call itself).
//
// repoPool and health are separate parameters even though production
// passes the same *pgxpool.Pool for both, so a test can hand repoPool a
// transaction instead (db.PGX, the same testutil.NewTx pattern every
// other package's tests already use for isolation) while giving health
// a trivial stub — pgx.Tx has no Ping method of its own to satisfy
// pinger.
func buildRouter(cfg config.Config, repoPool db.PGX, health pinger, telegramSender telegram.Sender, telegramClient authapi.TelegramMessenger) (router http.Handler, authHandler *authapi.Handler, usersService *users.Service, err error) {
	rolesRepo := roles.NewRepository(repoPool)
	rolesService := roles.NewService(rolesRepo)

	membershipsRepo := memberships.NewRepository(repoPool)
	membershipsService := memberships.NewService(membershipsRepo)

	boardRepo := board.NewRepository(repoPool)
	boardService := board.NewService(boardRepo)

	licensesRepo := licenses.NewRepository(repoPool)
	licensesService := licenses.NewService(licensesRepo, membershipsService)

	usersRepo := users.NewRepository(repoPool)
	usersService = users.NewService(usersRepo, rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(usersService)

	tokensRepo := tokens.NewRepository(repoPool)
	tokensService := tokens.NewService(tokensRepo, rolesService)

	authenticator := auth.NewAuthenticator(tokensService)

	activitiesRepo := activities.NewRepository(repoPool)
	activitiesService := activities.NewService(activitiesRepo, licensesService)

	inventoryRepo := inventory.NewRepository(repoPool)
	inventoryService := inventory.NewService(inventoryRepo)

	libraryService, err := library.NewService(cfg.LibraryRoot)
	if err != nil {
		slog.Error("set up document library", "error", err, "root", cfg.LibraryRoot)
		return nil, nil, nil, err
	}

	userFilesService, err := userfiles.NewService(cfg.UserFilesRoot)
	if err != nil {
		slog.Error("set up user files", "error", err, "root", cfg.UserFilesRoot)
		return nil, nil, nil, err
	}

	activityResourcesStorage, err := activityresources.NewStorage(cfg.ActivityResourcesRoot)
	if err != nil {
		slog.Error("set up activity resources storage", "error", err, "root", cfg.ActivityResourcesRoot)
		return nil, nil, nil, err
	}
	activityResourcesRepo := activityresources.NewRepository(repoPool)
	activityResourcesService := activityresources.NewService(activityResourcesRepo, activityResourcesStorage)

	accountingRepo := accounting.NewRepository(repoPool)
	accountingService := accounting.NewService(accountingRepo)

	mailer := mail.NewSenderFromConfig(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)

	authHandler = authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, telegramSender, telegramClient, authapi.Options{
		TokenTTL:              cfg.TokenTTL,
		MagicLinkTTL:          cfg.MagicLinkTTL,
		MagicLinkCooldown:     cfg.MagicLinkCooldown,
		TelegramBotDMLinkTTL:  cfg.TelegramBotDMLinkTTL,
		PublicBaseURL:         cfg.PublicBaseURL,
		TelegramBotUsername:   cfg.TelegramBotUsername,
		MagicLinkIPRateLimit:  cfg.MagicLinkIPRateLimit,
		MagicLinkIPRateWindow: cfg.MagicLinkIPRateWindow,
	})

	router = httpapi.NewRouter(httpapi.Routes{
		Health:            httpapi.NewHealthHandler(health),
		Auth:              authHandler,
		Users:             users.NewHandler(usersService, authenticator, telegramSender),
		Roles:             roles.NewHandler(rolesService, authenticator, usersService, telegramSender),
		Tokens:            tokens.NewHandler(tokensService, authenticator, usersService, cfg.ImpersonationTokenTTL, telegramSender, cfg.PublicBaseURL),
		Memberships:       memberships.NewHandler(membershipsService, authenticator, usersService, telegramSender),
		Licenses:          licenses.NewHandler(licensesService, authenticator, usersService, telegramSender),
		Activities:        activities.NewHandler(activitiesService, authenticator, usersService, telegramSender),
		Inventory:         inventory.NewHandler(inventoryService, authenticator, usersService, telegramSender),
		Board:             board.NewHandler(boardService, authenticator, usersService, telegramSender),
		Library:           library.NewHandler(libraryService, authenticator),
		UserFiles:         userfiles.NewHandler(userFilesService, authenticator, usersService),
		ActivityResources: activityresources.NewHandler(activityResourcesService, authenticator, activitiesService, usersService, telegramSender),
		Accounting:        accounting.NewHandler(accountingService, authenticator, usersService, telegramSender),
	})

	return router, authHandler, usersService, nil
}

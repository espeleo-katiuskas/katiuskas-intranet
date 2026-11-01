// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	// TokenTTL is how long a session lasts after its LAST use (30 days): every use renews it.
	TokenTTL          time.Duration
	MagicLinkTTL      time.Duration
	MagicLinkCooldown time.Duration
	// MagicLinkIPRateLimit/MagicLinkIPRateWindow throttle POST
	// /auth/magic-link and POST /auth/magic-link/consume-code per
	// calling address, sharing one limiter/budget across both (see
	// httpapi.IPRateLimiter) — a coarser backstop above
	// MagicLinkCooldown, which is keyed by the matched person instead:
	// cooldown alone does nothing to slow down one address cycling
	// through many different fake email/phone guesses, since each one
	// is a different "person" as far as the cooldown is concerned.
	MagicLinkIPRateLimit  int
	MagicLinkIPRateWindow time.Duration
	// TelegramBotDMLinkTTL is the validity authapi.Handler.notifyMagicLinkViaTelegram
	// states in, and the delay before it deletes, a magic-link DM sent in
	// direct reply to someone typing their email into the bot's own
	// chat — deliberately much shorter than MagicLinkTTL (the link's
	// real, unaffected expiry): that person is live in the chat right
	// now, so a tight window reads as more trustworthy than the same
	// span an email — read who knows when — gets, and shrinks how long
	// a still-valid-looking link sits exposed in the chat history. Only
	// that one specific DM uses this; every other magic-link message
	// (the email, and a DM merely riding along a web request) still
	// states, and is bound by, the real MagicLinkTTL.
	TelegramBotDMLinkTTL time.Duration
	// LocalTokenTTL is how long a session lasts after its last use when it is opened
	// through an address whose first label is "local" (local.intranet.katiuskas.es, the
	// one the club's shared tablet uses), instead of TokenTTL. See
	// authapi.Handler.sessionTTL.
	LocalTokenTTL time.Duration
	// ImpersonationTokenTTL is deliberately much shorter than TokenTTL —
	// a session acting as someone else is a bigger blast radius than a
	// normal one, so it expires on its own well before a normal session
	// would, rather than lingering for the full 30 days if someone
	// forgets to exit. It is also the one kind of session that does not
	// slide: its expiry is fixed, and using it does not move it.
	ImpersonationTokenTTL time.Duration
	// PublicBaseURL is where the frontend is served, used to build the
	// magic-link URL a sign-in email points to (e.g. https://intranet.club).
	PublicBaseURL string
	SMTPHost      string
	SMTPPort      string
	SMTPUsername  string
	SMTPPassword  string
	SMTPFrom      string
	// TelegramBotToken/TelegramLogChatID configure forwarding the API's
	// own error-level logs to a Telegram group or channel (see
	// internal/telegram.LogHandler) — same optional, logs-instead-of-
	// sending-when-unset story as SMTP above. TelegramLogChatID is
	// whatever Telegram itself uses to address that chat: a negative
	// number for a group/supergroup, or "@name" for a public
	// channel/group's username.
	TelegramBotToken  string
	TelegramLogChatID string
	// TelegramBotUsername is the bot's public "@name" (without the "@"),
	// e.g. "KatiuskasBot" — unlike TelegramBotToken, this isn't secret;
	// it's only used to build a t.me/<username> link on the login page
	// offering Telegram as a way to sign in (see
	// authapi.Handler.TelegramBotInfo). Left empty, that option simply
	// doesn't show up, the same as every other Telegram feature being
	// unset.
	TelegramBotUsername string
	// LibraryRoot is the directory the internal/library package serves
	// club documents (estatutos, actas...) from — mounted read-only into
	// the container, populated by whoever deploys the club's documents,
	// never written to by the API itself.
	LibraryRoot string
	// UserFilesRoot is the directory internal/userfiles serves each
	// person's own documents from, one subdirectory per login — same
	// deployment story as LibraryRoot (mounted read-only, populated by
	// whoever deploys the club's documents, never written to by the API
	// itself), just scoped per person instead of one shared tree.
	UserFilesRoot string
	// ActivityResourcesRoot is the directory internal/activityresources
	// saves a memoria's uploaded files to, one subdirectory per activity
	// — unlike LibraryRoot/UserFilesRoot, this one *is* written to by
	// the API itself (an actual upload endpoint, not a read-only,
	// drop-in-by-hand tree), so it's backed by a named Docker volume
	// rather than a host bind mount (see docker-compose.yml).
	ActivityResourcesRoot string
	// TransactionFilesRoot is the directory internal/transactionfiles saves the documents
	// (tickets, invoices) attached to transactions to, one subdirectory per transaction. Like
	// ActivityResourcesRoot it is written to by the API, so it is backed by a named volume.
	TransactionFilesRoot string
	// DiskAlertPercent is the share of a filesystem holding LibraryRoot,
	// UserFilesRoot, ActivityResourcesRoot or TransactionFilesRoot that may be used before the
	// monitor (adminstatus.Monitor) warns that it is almost full; 0 turns
	// that warning off. Set with DISK_ALERT_PERCENT, 90 by default.
	DiskAlertPercent int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              getEnv("HTTP_ADDR", ":64711"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		TokenTTL:              30 * 24 * time.Hour,
		LocalTokenTTL:         3 * time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     60 * time.Second,
		MagicLinkIPRateLimit:  20,
		MagicLinkIPRateWindow: 15 * time.Minute,
		TelegramBotDMLinkTTL:  5 * time.Minute,
		ImpersonationTokenTTL: time.Hour,
		PublicBaseURL:         getEnv("PUBLIC_BASE_URL", "http://localhost:64710"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              getEnv("SMTP_PORT", "587"),
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("SMTP_FROM"),
		TelegramBotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramLogChatID:     os.Getenv("TELEGRAM_LOG_CHAT_ID"),
		TelegramBotUsername:   os.Getenv("TELEGRAM_BOT_USERNAME"),
		LibraryRoot:           getEnv("LIBRARY_ROOT", "/data/library"),
		UserFilesRoot:         getEnv("USER_FILES_ROOT", "/data/user-files"),
		ActivityResourcesRoot: getEnv("ACTIVITY_RESOURCES_ROOT", "/data/activity-resources"),
		TransactionFilesRoot:  getEnv("TRANSACTION_FILES_ROOT", "/data/transaction-files"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	percent, err := strconv.Atoi(getEnv("DISK_ALERT_PERCENT", "90"))
	if err != nil || percent < 0 || percent > 100 {
		return cfg, fmt.Errorf("DISK_ALERT_PERCENT must be a number from 0 to 100")
	}
	cfg.DiskAlertPercent = percent
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

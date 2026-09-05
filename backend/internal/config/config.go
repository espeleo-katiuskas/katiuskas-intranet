// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	TokenTTL          time.Duration
	MagicLinkTTL      time.Duration
	MagicLinkCooldown time.Duration
	// ImpersonationTokenTTL is deliberately much shorter than TokenTTL —
	// a session acting as someone else is a bigger blast radius than a
	// normal one, so it expires on its own well before a normal session
	// would, rather than lingering for the full 30 days if someone
	// forgets to exit.
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
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              getEnv("HTTP_ADDR", ":64711"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		TokenTTL:              30 * 24 * time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     60 * time.Second,
		ImpersonationTokenTTL: time.Hour,
		PublicBaseURL:         getEnv("PUBLIC_BASE_URL", "http://localhost:64710"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              getEnv("SMTP_PORT", "587"),
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("SMTP_FROM"),
		TelegramBotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramLogChatID:     os.Getenv("TELEGRAM_LOG_CHAT_ID"),
		LibraryRoot:           getEnv("LIBRARY_ROOT", "/data/library"),
		UserFilesRoot:         getEnv("USER_FILES_ROOT", "/data/user-files"),
		ActivityResourcesRoot: getEnv("ACTIVITY_RESOURCES_ROOT", "/data/activity-resources"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

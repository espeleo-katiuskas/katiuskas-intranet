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
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:              getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		TokenTTL:              30 * 24 * time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     60 * time.Second,
		ImpersonationTokenTTL: time.Hour,
		PublicBaseURL:         getEnv("PUBLIC_BASE_URL", "http://localhost:8000"),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              getEnv("SMTP_PORT", "587"),
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("SMTP_FROM"),
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

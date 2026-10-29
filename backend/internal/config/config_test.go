package config

import "testing"

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when DATABASE_URL is missing")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("LIBRARY_ROOT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":64711" {
		t.Errorf("expected default HTTPAddr :64711, got %q", cfg.HTTPAddr)
	}
	if cfg.PublicBaseURL != "http://localhost:64710" {
		t.Errorf("expected default PublicBaseURL, got %q", cfg.PublicBaseURL)
	}
	if cfg.SMTPPort != "587" {
		t.Errorf("expected default SMTPPort 587, got %q", cfg.SMTPPort)
	}
	if cfg.SMTPHost != "" {
		t.Errorf("expected SMTPHost to have no default, got %q", cfg.SMTPHost)
	}
	if cfg.LibraryRoot != "/data/library" {
		t.Errorf("expected default LibraryRoot /data/library, got %q", cfg.LibraryRoot)
	}
}

func TestLoadHonorsOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PUBLIC_BASE_URL", "https://intranet.example")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("LIBRARY_ROOT", "/srv/documents")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("expected overridden HTTPAddr, got %q", cfg.HTTPAddr)
	}
	if cfg.PublicBaseURL != "https://intranet.example" {
		t.Errorf("expected overridden PublicBaseURL, got %q", cfg.PublicBaseURL)
	}
	if cfg.SMTPHost != "smtp.example.com" {
		t.Errorf("expected overridden SMTPHost, got %q", cfg.SMTPHost)
	}
	if cfg.SMTPPort != "2525" {
		t.Errorf("expected overridden SMTPPort, got %q", cfg.SMTPPort)
	}
	if cfg.LibraryRoot != "/srv/documents" {
		t.Errorf("expected overridden LibraryRoot, got %q", cfg.LibraryRoot)
	}
}

func TestLoadDiskAlertPercent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	t.Setenv("DISK_ALERT_PERCENT", "")
	if cfg, err := Load(); err != nil || cfg.DiskAlertPercent != 90 {
		t.Errorf("expected a default of 90, got %d (%v)", cfg.DiskAlertPercent, err)
	}
	t.Setenv("DISK_ALERT_PERCENT", "0")
	if cfg, err := Load(); err != nil || cfg.DiskAlertPercent != 0 {
		t.Errorf("expected 0 (off) to be accepted, got %d (%v)", cfg.DiskAlertPercent, err)
	}
	for _, bad := range []string{"abc", "101", "-1"} {
		t.Setenv("DISK_ALERT_PERCENT", bad)
		if _, err := Load(); err == nil {
			t.Errorf("expected %q to be refused", bad)
		}
	}
}

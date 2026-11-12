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
	t.Setenv("ARCHIVE_ROOT", "")
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
	if cfg.ArchiveRoot != "/data/archive" {
		t.Errorf("expected default ArchiveRoot /data/archive, got %q", cfg.ArchiveRoot)
	}
}

func TestLoadHonorsOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PUBLIC_BASE_URL", "https://intranet.example")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("ARCHIVE_ROOT", "/srv/documents")

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
	if cfg.ArchiveRoot != "/srv/documents" {
		t.Errorf("expected overridden ArchiveRoot, got %q", cfg.ArchiveRoot)
	}
}

// An api.env written when the archive of documents was still called the library keeps working.
func TestLoadArchiveRootFallsBackToTheOldName(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("ARCHIVE_ROOT", "")
	t.Setenv("LIBRARY_ROOT", "/srv/old")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ArchiveRoot != "/srv/old" {
		t.Errorf("expected LIBRARY_ROOT to be used, got %q", cfg.ArchiveRoot)
	}
	t.Setenv("ARCHIVE_ROOT", "/srv/new")
	if cfg, _ = Load(); cfg.ArchiveRoot != "/srv/new" {
		t.Errorf("expected ARCHIVE_ROOT to win, got %q", cfg.ArchiveRoot)
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

// The directory of the items' files is LIBRARY_FILES_ROOT, and when that is not set one called library-files
// beside the memorias': an installation whose environment file predates the library still starts.
func TestLoadLibraryFilesRoot(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	t.Setenv("ACTIVITY_RESOURCES_ROOT", "")
	t.Setenv("LIBRARY_FILES_ROOT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LibraryFilesRoot != "/data/library-files" {
		t.Errorf("expected the default /data/library-files, got %q", cfg.LibraryFilesRoot)
	}

	t.Setenv("ACTIVITY_RESOURCES_ROOT", "/opt/katiuskas/data/activity-resources")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LibraryFilesRoot != "/opt/katiuskas/data/library-files" {
		t.Errorf("expected it beside the memorias, got %q", cfg.LibraryFilesRoot)
	}

	t.Setenv("LIBRARY_FILES_ROOT", "/srv/ejemplares")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LibraryFilesRoot != "/srv/ejemplares" {
		t.Errorf("expected the one that was set, got %q", cfg.LibraryFilesRoot)
	}
}

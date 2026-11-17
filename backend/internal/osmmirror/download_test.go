package osmmirror_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
)

// A download is made only when the file changed, keeps the date of the server, says who calls, and a
// download that breaks leaves the old file alone.
func TestDownload(t *testing.T) {
	modified := time.Date(2026, 11, 16, 21, 22, 36, 0, time.UTC)
	var body = []byte("the extract")
	var requests, broken int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("User-Agent") != osmmirror.UserAgent {
			t.Errorf("unexpected client name %q", r.Header.Get("User-Agent"))
		}
		if since, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !modified.After(since) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if broken > 0 {
			broken--
			w.Header().Set("Content-Length", "100") // Says more than it sends.
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
		_, _ = w.Write(body)
	}))
	defer server.Close()
	dir := t.TempDir()
	ctx := context.Background()

	path, fresh, err := osmmirror.Download(ctx, server.Client(), server.URL, dir, "x.osm.pbf")
	if err != nil || !fresh || path != filepath.Join(dir, "x.osm.pbf") {
		t.Fatalf("first download: %q %v %v", path, fresh, err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(modified) {
		t.Errorf("the file keeps the date of the server: %v %v", info.ModTime(), err)
	}
	if _, fresh, err := osmmirror.Download(ctx, server.Client(), server.URL, dir, "x.osm.pbf"); err != nil || fresh {
		t.Errorf("a file that did not change is not downloaded again: %v %v", fresh, err)
	}

	// A new version that breaks half way: an error, and the old file is still there and whole.
	modified = modified.Add(time.Hour)
	broken = 1
	if _, _, err := osmmirror.Download(ctx, server.Client(), server.URL, dir, "x.osm.pbf"); err == nil {
		t.Error("expected an error for a short download")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "the extract" {
		t.Errorf("the old file must stay: %q %v", data, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("no leftover of the failed download expected, got %d files", len(entries))
	}
	if _, fresh, err := osmmirror.Download(ctx, server.Client(), server.URL, dir, "x.osm.pbf"); err != nil || !fresh {
		t.Errorf("the next try gets it: %v %v", fresh, err)
	}
}

func TestDownloadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, _, err := osmmirror.Download(context.Background(), server.Client(), server.URL, t.TempDir(), "x"); err == nil {
		t.Error("expected an error for a 500")
	}
}

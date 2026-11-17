package osmmirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// UserAgent is what the downloads say: who calls and nothing more (no person, no project).
const UserAgent = "intranet-osm-mirror/1.0"

// Download brings the file at url into dir, as name, unless the one already there is as new: it asks with
// If-Modified-Since, so a file that did not change costs one request and no bytes. It says whether there is
// a new file. The new one is written beside the old and swapped in when whole, so a download that fails
// leaves the old one alone, and it carries the server's date as its modification time.
func Download(ctx context.Context, client *http.Client, url, dir, name string) (path string, fresh bool, err error) {
	path = filepath.Join(dir, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if info, statErr := os.Stat(path); statErr == nil {
		req.Header.Set("If-Modified-Since", info.ModTime().UTC().Format(http.TimeFormat))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return path, false, nil
	case http.StatusOK:
	default:
		return "", false, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(dir, name+".part-")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, resp.Body)
	if err == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
		err = fmt.Errorf("download %s: got %d bytes of %d", url, n, resp.ContentLength)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", false, err
	}
	if modified, parseErr := http.ParseTime(resp.Header.Get("Last-Modified")); parseErr == nil {
		_ = os.Chtimes(tmp.Name(), time.Now(), modified)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", false, err
	}
	return path, true, nil
}

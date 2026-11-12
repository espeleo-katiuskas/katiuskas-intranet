package archive_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/archive"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "Estatutos.pdf"), "%PDF-1.4 fake pdf content")
	if err := os.MkdirAll(filepath.Join(root, "Actas"), 0o755); err != nil {
		t.Fatalf("mkdir Actas: %v", err)
	}
	write(t, filepath.Join(root, "Actas", "2024-acta.md"), "# Acta 2024\n\nContenido.")

	svc, err := archive.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	archive.NewHandler(svc, authenticator).RegisterRoutes(mux)
	return mux, resolver
}

func doRequest(mux *http.ServeMux, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func doRequestWithCookie(mux *http.ServeMux, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerListRequiresAuthentication(t *testing.T) {
	mux, _ := newTestServer(t)
	if rec := doRequest(mux, "/api/v1/archive", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerListIsOpenToAnyBaselineRole(t *testing.T) {
	mux, authn := newTestServer(t)
	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(authn, 1, role)
		rec := doRequest(mux, "/api/v1/archive", token)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for role %q, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerListRejectsNonBaselineRole(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "material")
	if rec := doRequest(mux, "/api/v1/archive", token); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandlerListReturnsEntries(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Actas"`) || !strings.Contains(rec.Body.String(), `"name":"Estatutos.pdf"`) {
		t.Errorf("expected both entries in the listing, got %s", rec.Body.String())
	}
}

func TestHandlerListNestedPath(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive?path=Actas", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"2024-acta.md"`) {
		t.Errorf("expected 2024-acta.md in the listing, got %s", rec.Body.String())
	}
}

func TestHandlerListNotFound(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive?path=nope", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileServesContentInlineByDefault(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive/file/Actas/2024-acta.md", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "# Acta 2024\n\nContenido." {
		t.Errorf("expected the raw file content, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("expected markdown content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="2024-acta.md"` {
		t.Errorf("expected inline disposition, got %q", got)
	}
}

func TestHandlerFileDownloadSetsAttachmentDisposition(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive/file/Estatutos.pdf?download=1", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="Estatutos.pdf"` {
		t.Errorf("expected attachment disposition, got %q", got)
	}
}

func TestHandlerFileNotFound(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive/file/nope.pdf", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileRejectsADirectoryPath(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/archive/file/Actas", token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerFileRequiresAuthentication(t *testing.T) {
	mux, _ := newTestServer(t)
	if rec := doRequest(mux, "/api/v1/archive/file/Estatutos.pdf", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestHandlerFileAcceptsSessionCookie is what makes it possible to open
// a document straight in a new tab (or let the browser's own "Save As"
// use the real filename) — a plain GET can't carry a custom
// Authorization header, so /file also accepts the same token as a
// cookie (see auth.ProtectAllowingCookie).
func TestHandlerFileAcceptsSessionCookie(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequestWithCookie(mux, "/api/v1/archive/file/Estatutos.pdf", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with a cookie-only request, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerListRejectsSessionCookie is the boundary check: only
// /file opts into the cookie fallback. /archive's own listing must
// keep requiring the bearer header, so cookie support never quietly
// widens beyond the specific routes that actually need it.
func TestHandlerListRejectsSessionCookie(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequestWithCookie(mux, "/api/v1/archive", token)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (GET /archive must not accept the session cookie), got %d", rec.Code)
	}
}

// If the root of the archive is gone, the API says it is unavailable (503), not that the path does not
// exist (404), so that the page can tell a problem of the server from a wrong address.
func TestHandlerAMissingRootAnswers503(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	svc, err := archive.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	authenticator, resolver := testutil.NewTestAuthenticator()
	mux := http.NewServeMux()
	archive.NewHandler(svc, authenticator).RegisterRoutes(mux)
	token := testutil.TokenFor(resolver, 1, "member")
	if err := os.Remove(root); err != nil {
		t.Fatalf("remove root: %v", err)
	}
	for _, path := range []string{"/api/v1/archive", "/api/v1/archive?path=Actas", "/api/v1/archive/file/Estatutos.pdf"} {
		rec := doRequest(mux, path, token)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "archive_unavailable") {
			t.Errorf("%s: expected 503 archive_unavailable, got %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

package library_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"katiuskas-intranet/backend/internal/library"
	"katiuskas-intranet/backend/internal/testutil"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "Estatutos.pdf"), "%PDF-1.4 fake pdf content")
	if err := os.MkdirAll(filepath.Join(root, "Actas"), 0o755); err != nil {
		t.Fatalf("mkdir Actas: %v", err)
	}
	write(t, filepath.Join(root, "Actas", "2024-acta.md"), "# Acta 2024\n\nContenido.")

	svc, err := library.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	library.NewHandler(svc, authenticator).RegisterRoutes(mux)
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

func TestHandlerListRequiresAuthentication(t *testing.T) {
	mux, _ := newTestServer(t)
	if rec := doRequest(mux, "/api/v1/library", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerListIsOpenToAnyBaselineRole(t *testing.T) {
	mux, authn := newTestServer(t)
	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(authn, 1, role)
		rec := doRequest(mux, "/api/v1/library", token)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for role %q, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerListRejectsNonBaselineRole(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "material")
	if rec := doRequest(mux, "/api/v1/library", token); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandlerListReturnsEntries(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/library", token)
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

	rec := doRequest(mux, "/api/v1/library?path=Actas", token)
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

	rec := doRequest(mux, "/api/v1/library?path=nope", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileServesContentInlineByDefault(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/library/file?path=Actas%2F2024-acta.md", token)
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

	rec := doRequest(mux, "/api/v1/library/file?path=Estatutos.pdf&download=1", token)
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

	rec := doRequest(mux, "/api/v1/library/file?path=nope.pdf", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileRejectsADirectoryPath(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/library/file?path=Actas", token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerFileRequiresAuthentication(t *testing.T) {
	mux, _ := newTestServer(t)
	if rec := doRequest(mux, "/api/v1/library/file?path=Estatutos.pdf", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}


package userfiles_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/userfiles"
)

// fakeUsers is the minimal userfiles.UserLookup a handler test needs —
// a fixed login-to-id map, no database involved.
type fakeUsers struct {
	idsByLogin map[string]int64
}

func (f fakeUsers) UserIDForLogin(_ context.Context, login string) (int64, error) {
	id, ok := f.idsByLogin[login]
	if !ok {
		return 0, userfiles.ErrNotFound
	}
	return id, nil
}

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "juan.perez", "DNI.pdf"), "%PDF-1.4 fake pdf content")
	if err := os.MkdirAll(filepath.Join(root, "juan.perez", "Licencias"), 0o755); err != nil {
		t.Fatalf("mkdir Licencias: %v", err)
	}
	write(t, filepath.Join(root, "juan.perez", "Licencias", "2024-federativa.md"), "# Federativa 2024\n\nContenido.")

	svc, err := userfiles.NewService(root)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	authenticator, resolver := testutil.NewTestAuthenticator()
	users := fakeUsers{idsByLogin: map[string]int64{"juan.perez": 1, "maria.gomez": 2}}

	mux := http.NewServeMux()
	userfiles.NewHandler(svc, authenticator, users).RegisterRoutes(mux)
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
	if rec := doRequest(mux, "/api/v1/users/juan.perez/files", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerListAllowsThePersonThemselves(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")
	rec := doRequest(mux, "/api/v1/users/juan.perez/files", token)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListAllowsAdminAndBoardForAnyone(t *testing.T) {
	mux, authn := newTestServer(t)
	for _, role := range []string{"admin", "board"} {
		token := testutil.TokenFor(authn, 99, role)
		rec := doRequest(mux, "/api/v1/users/juan.perez/files", token)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for role %q, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerListRejectsAnotherPlainMember(t *testing.T) {
	mux, authn := newTestServer(t)
	// id 2 is maria.gomez, requesting juan.perez's files.
	token := testutil.TokenFor(authn, 2, "member")
	rec := doRequest(mux, "/api/v1/users/juan.perez/files", token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListRejectsNonBaselineRole(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "material")
	if rec := doRequest(mux, "/api/v1/users/juan.perez/files", token); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandlerListUnknownLoginIsNotFound(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "admin")
	rec := doRequest(mux, "/api/v1/users/nobody/files", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListReturnsEntries(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Licencias"`) || !strings.Contains(rec.Body.String(), `"name":"DNI.pdf"`) {
		t.Errorf("expected both entries in the listing, got %s", rec.Body.String())
	}
}

func TestHandlerListNestedPath(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files?path=Licencias", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"2024-federativa.md"`) {
		t.Errorf("expected 2024-federativa.md in the listing, got %s", rec.Body.String())
	}
}

// maria.gomez has no subdirectory of her own in this fixture — an empty
// listing (200), not a 404, since that's the common case for most
// people.
func TestHandlerListEmptyForAPersonWithNoFilesYet(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 2, "member")

	rec := doRequest(mux, "/api/v1/users/maria.gomez/files", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("expected an empty entries array, got %s", rec.Body.String())
	}
}

func TestHandlerListNotFoundForASpecificSubdirectory(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files?path=nope", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileServesContentInlineByDefault(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/Licencias/2024-federativa.md", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "# Federativa 2024\n\nContenido." {
		t.Errorf("expected the raw file content, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("expected markdown content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="2024-federativa.md"` {
		t.Errorf("expected inline disposition, got %q", got)
	}
}

func TestHandlerFileDownloadSetsAttachmentDisposition(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/DNI.pdf?download=1", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="DNI.pdf"` {
		t.Errorf("expected attachment disposition, got %q", got)
	}
}

func TestHandlerFileRejectsAnotherPlainMember(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 2, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/DNI.pdf", token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandlerFileNotFound(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/nope.pdf", token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerFileRejectsADirectoryPath(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/Licencias", token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerFileRequiresAuthentication(t *testing.T) {
	mux, _ := newTestServer(t)
	if rec := doRequest(mux, "/api/v1/users/juan.perez/files/file/DNI.pdf", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestHandlerFileAcceptsSessionCookie mirrors archive's own test: /file
// also accepts the session cookie so a person's own document can be
// opened as a plain, browser-navigable URL — and resolveAccess's
// self-or-admin/board check still applies, since the cookie populates
// the same AuthenticatedUser in context as the bearer header does.
func TestHandlerFileAcceptsSessionCookie(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequestWithCookie(mux, "/api/v1/users/juan.perez/files/file/DNI.pdf", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with a cookie-only request, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerFileCookieStillEnforcesSelfOrPrivileged checks the cookie
// fallback doesn't bypass resolveAccess's own access check — someone
// else's cookie-only request for juan.perez's file must still be 403.
func TestHandlerFileCookieStillEnforcesSelfOrPrivileged(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 2, "member")

	rec := doRequestWithCookie(mux, "/api/v1/users/juan.perez/files/file/DNI.pdf", token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// TestHandlerListRejectsSessionCookie is the boundary check: only
// /file opts into the cookie fallback, not /files itself.
func TestHandlerListRejectsSessionCookie(t *testing.T) {
	mux, authn := newTestServer(t)
	token := testutil.TokenFor(authn, 1, "member")

	rec := doRequestWithCookie(mux, "/api/v1/users/juan.perez/files", token)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (GET .../files must not accept the session cookie), got %d", rec.Code)
	}
}

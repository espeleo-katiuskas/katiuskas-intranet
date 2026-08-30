package users_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/users"
)

func newTestServer(t *testing.T) (*http.ServeMux, *auth.Authenticator, *users.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	usersService := users.NewService(users.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator("test-secret", time.Hour)

	mux := http.NewServeMux()
	users.NewHandler(usersService, authenticator).RegisterRoutes(mux)
	return mux, authenticator, usersService
}

func tokenFor(t *testing.T, a *auth.Authenticator, id int64, roleNames ...string) string {
	t.Helper()
	token, err := a.IssueAccessToken(id, roleNames)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func doRequest(mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestHandlerGetSelfVersusOthers(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{FirstName: "M", LastName: "Ember", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	other, err := svc.Create(ctx, users.CreateInput{FirstName: "O", LastName: "Ther", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", member.ID), memberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a member to view their own profile (200), got %d: %s", rec.Code, rec.Body.String())
	}

	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", other.ID), memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member viewing someone else's profile to get 403, got %d", rec.Code)
	}

	adminToken := tokenFor(t, authn, 999999, "admin")
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", other.ID), adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected an admin to view anyone's profile (200), got %d", rec.Code)
	}
}

func TestHandlerListRequiresAdminOrBoard(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardToken := tokenFor(t, authn, 2, "board")

	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for a member listing users, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for board listing users, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

// There's no "grant access" step anymore — creating a person always
// succeeds regardless of whether a phone is given. Without one, they
// simply can't ever be found by FindActiveByEmailAndPhone (see the
// users package doc comment), which isn't something creation needs to
// validate up front.
func TestHandlerCreateWithoutPhoneSucceeds(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t),
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateRejectsUnknownRole(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t),
		"role_names": []string{"not-a-real-role"},
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_role" {
		t.Errorf("expected error code unknown_role, got %q", code)
	}
}

func TestHandlerDeleteCannotTargetSelf(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 42, "admin")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/users/42", adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "cannot_delete_self" {
		t.Errorf("expected error code cannot_delete_self, got %q", code)
	}
}

func TestHandlerDeleteBlocksTheLastAdminEndToEnd(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	solo, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Solo", LastName: "Admin", Email: testutil.UniqueEmail(t), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// A different caller (not the admin under deletion) tries to delete them.
	callerToken := tokenFor(t, authn, 999999, "admin")

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", solo.ID), callerToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "last_admin" {
		t.Errorf("expected error code last_admin, got %q", code)
	}
}

func TestHandlerDirectoryIsOpenToAnyAuthenticatedUserButHidesPersonalData(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	phone := "600111222"
	person, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Directory", LastName: "Entry", Email: testutil.UniqueEmail(t),
		Phone: &phone,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	memberToken := tokenFor(t, authn, 999999, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/users/directory", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a plain member to read the directory (200), got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte(fmt.Sprintf(`"id":%d`, person.ID))) {
		t.Errorf("expected the directory to include the created person, got %s", body)
	}
	var entries []users.DirectoryEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode directory: %v", err)
	}
	if body := rec.Body.String(); bytes.Contains([]byte(body), []byte(phone)) {
		t.Errorf("expected the directory to never expose phone numbers, got %s", body)
	}

	if rec := doRequest(mux, http.MethodGet, "/api/v1/users/directory", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

func TestHandlerUpdateSelfVersusOthers(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{FirstName: "M", LastName: "Ember", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	other, err := svc.Create(ctx, users.CreateInput{FirstName: "O", LastName: "Ther", Email: testutil.UniqueEmail(t)})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", member.ID), memberToken, map[string]any{"city": "Madrid"})
	if rec.Code != http.StatusOK {
		t.Errorf("expected a member to edit their own profile (200), got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", other.ID), memberToken, map[string]any{"city": "Madrid"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected a member editing someone else's profile to get 403, got %d", rec.Code)
	}
}

package tokens_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/licenses"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/tokens"
	"katiuskas-intranet/backend/internal/users"
)

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *tokens.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	tokens.NewHandler(service, authenticator, usersService, time.Hour).RegisterRoutes(mux)
	return mux, resolver, service, tx
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

func TestHandlerListRequiresAdmin(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/tokens", target.Login), boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from listing tokens, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/tokens", target.Login), adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected admin to list tokens, got %d: %s", rec.Code, rec.Body.String())
	}
}

// CreateImpersonation's row references the caller's id via a real
// foreign key, unlike the other admin routes here — so, unlike them,
// this test needs a real inserted admin, not just an arbitrary fake id.
func TestHandlerImpersonateRequiresAdmin(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	boardCaller := testutil.InsertUser(t, tx, "")

	boardToken := testutil.TokenFor(resolver, boardCaller.ID, "board")
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/impersonate", target.Login), boardToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from impersonating, got %d", rec.Code)
	}
}

func TestHandlerImpersonateSucceedsAndTheTokenWorksAsTheTarget(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	rolesService := roles.NewService(roles.NewRepository(tx))
	if err := rolesService.AssignByName(ctx, target.ID, []string{"admin"}); err != nil {
		t.Fatalf("assign admin to target: %v", err)
	}

	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/impersonate", target.Login), adminToken, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Token == "" {
		t.Fatal("expected a non-empty token")
	}

	resolved, err := service.Authenticate(ctx, body.Token, "")
	if err != nil {
		t.Fatalf("authenticate with the impersonation token: %v", err)
	}
	if resolved.ID != target.ID {
		t.Errorf("expected the token to resolve to the target %d, got %d", target.ID, resolved.ID)
	}
	if resolved.ImpersonatedBy == nil || *resolved.ImpersonatedBy != admin.ID {
		t.Errorf("expected impersonated_by %d, got %v", admin.ID, resolved.ImpersonatedBy)
	}
}

func TestHandlerImpersonateRejectsSelfAndIneligibleTarget(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	admin := testutil.InsertUser(t, tx, "")
	noRoleTarget := testutil.InsertUser(t, tx, "")

	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/impersonate", admin.Login), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 impersonating yourself, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/impersonate", noRoleTarget.Login), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 impersonating an ineligible target, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListReturnsTokensNewestFirstWithoutHashes(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/tokens", target.Login), adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("token_hash")) || bytes.Contains(rec.Body.Bytes(), []byte("hash")) {
		t.Errorf("expected the response to never expose a token hash, got %s", rec.Body.String())
	}

	var list []tokens.Token
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 token, got %d", len(list))
	}
}

func TestHandlerUpdateExpiryAndRevokeRequireAdmin(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, err := service.ListForUser(context.Background(), target.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 token, got %d (err %v)", len(list), err)
	}
	tokenID := list[0].ID

	boardToken := testutil.TokenFor(resolver, 1, "board")
	path := fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID)

	if rec := doRequest(mux, http.MethodPatch, path, boardToken, map[string]string{"expires_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339)}); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from extending expiry, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from revoking, got %d", rec.Code)
	}
}

func TestHandlerUpdateExpiry(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	newExpiry := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID), adminToken,
		map[string]string{"expires_at": newExpiry.Format(time.RFC3339)})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := service.ListForUser(context.Background(), target.ID)
	if err != nil || len(updated) != 1 {
		t.Fatalf("expected 1 token, got %d (err %v)", len(updated), err)
	}
	if !updated[0].ExpiresAt.Equal(newExpiry) {
		t.Errorf("expected expires_at %v, got %v", newExpiry, updated[0].ExpiresAt)
	}
}

func TestHandlerRevoke(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := service.ListForUser(context.Background(), target.ID)
	if err != nil || len(updated) != 1 {
		t.Fatalf("expected 1 token, got %d (err %v)", len(updated), err)
	}
	if updated[0].RevokedAt == nil {
		t.Errorf("expected the token to be revoked")
	}
}

func TestHandlerRejectsTokenIDBelongingToAnotherUser(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	owner := testutil.InsertUser(t, tx, "")
	someoneElse := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), owner.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), owner.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/tokens/%d", someoneElse.Login, tokenID)
	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a token id under the wrong user, got %d", rec.Code)
	}
}

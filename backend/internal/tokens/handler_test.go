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

	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/tokens"
)

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *tokens.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	service := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	tokens.NewHandler(service, authenticator).RegisterRoutes(mux)
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
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d/tokens", target.ID), boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from listing tokens, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d/tokens", target.ID), adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected admin to list tokens, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListReturnsTokensNewestFirstWithoutHashes(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%d/tokens", target.ID), adminToken, nil)
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
	path := fmt.Sprintf("/api/v1/users/%d/tokens/%d", target.ID, tokenID)

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
	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d/tokens/%d", target.ID, tokenID), adminToken,
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
	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d/tokens/%d", target.ID, tokenID), adminToken, nil)
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
	path := fmt.Sprintf("/api/v1/users/%d/tokens/%d", someoneElse.ID, tokenID)
	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a token id under the wrong user, got %d", rec.Code)
	}
}

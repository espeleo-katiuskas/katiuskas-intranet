package tokens_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *tokens.Service, pgx.Tx) {
	mux, resolver, service, tx, _ := newHandlerTestServerWithNotifier(t, nil)
	return mux, resolver, service, tx
}

// newHandlerTestServerWithNotifier is newHandlerTestServer, but also
// wires notifier and returns the users.Service backing it (so a test
// can create an actor with a real, resolvable login) — pass nil (like
// newHandlerTestServer does) when a test doesn't care about mutation
// notifications.
func newHandlerTestServerWithNotifier(t *testing.T, notifier httpapi.MutationNotifier) (*http.ServeMux, testutil.FakeResolver, *tokens.Service, pgx.Tx, *users.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	tokens.NewHandler(service, authenticator, usersService, time.Hour, notifier, "https://intranet.example.test").RegisterRoutes(mux)
	return mux, resolver, service, tx, usersService
}

// fakeNotifier stands in for httpapi.MutationNotifier so tests can
// check what a mutation notification actually said, without a real
// Telegram call.
type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

// waitForMessage polls briefly for NotifyAsync's own goroutine to have
// delivered something, rather than asserting immediately after the
// request returns (the notification is fire-and-forget, so it can
// still be in flight by then).
func (f *fakeNotifier) waitForMessage(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a mutation notification")
	return ""
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

	resolved, err := service.Authenticate(ctx, body.Token, "", "")
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

// TestHandlerImpersonateSetsSessionCookie checks the impersonation
// token is also carried as auth.SessionCookieName, matching the
// response body — so a library/userfiles file opened straight from a
// new tab while impersonating resolves as the impersonated person too,
// the same as every other request the frontend makes with this token.
func TestHandlerImpersonateSetsSessionCookie(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
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

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName || cookies[0].Value != body.Token {
		t.Errorf("expected the session cookie to carry the impersonation token, got %v", cookies)
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

// TestHandlerUpdateExpiryRejectsMalformedBody covers Decode's own error
// path — doRequest always marshals a valid Go value into valid JSON, so
// this builds the request directly with a body that isn't JSON at all.
func TestHandlerUpdateExpiryRejectsMalformedBody(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID)
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid_body, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerUpdateExpiryRequiresExpiresAt covers the other half of the
// same validation: well-formed JSON, but missing (or zero) expires_at.
func TestHandlerUpdateExpiryRequiresExpiresAt(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID)
	rec := doRequest(mux, http.MethodPatch, path, adminToken, map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing_fields, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerPathIDsRejectsNonNumericTokenID covers pathIDs's own
// tokenID parse failure — shared by both updateExpiry and revoke, so
// exercising it through either route is enough for both.
func TestHandlerPathIDsRejectsNonNumericTokenID(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/tokens/not-a-number", target.Login)

	if rec := doRequest(mux, http.MethodPatch, path, adminToken, map[string]string{"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id for a non-numeric token id, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id for a non-numeric token id, got %d: %s", rec.Code, rec.Body.String())
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

func TestHandlerImpersonateNotifiesTelegram(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, resolver, _, tx, _ := newHandlerTestServerWithNotifier(t, notifier)
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

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor %q and target %q, got %q", admin.Login, target.Login, msg)
	}
}

func TestHandlerUpdateExpiryNotifiesTelegramWithOldAndNewExpiry(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, resolver, service, tx, _ := newHandlerTestServerWithNotifier(t, notifier)
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	newExpiry := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID), adminToken,
		map[string]string{"expires_at": newExpiry.Format(time.RFC3339)})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor %q and target %q, got %q", admin.Login, target.Login, msg)
	}
	if !strings.Contains(msg, newExpiry.Format("2006-01-02")) {
		t.Errorf("expected the notification to show the new expiry, got %q", msg)
	}
}

func TestHandlerRevokeNotifiesTelegram(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, resolver, service, tx, _ := newHandlerTestServerWithNotifier(t, notifier)
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(context.Background(), target.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	list, _ := service.ListForUser(context.Background(), target.ID)
	tokenID := list[0].ID

	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", target.Login, tokenID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor %q and target %q, got %q", admin.Login, target.Login, msg)
	}
}

// GET /tokens is the admin's overview of everyone's sessions: only the
// active ones by default (not revoked, not expired), most recently used
// first, each with its owner's login; ?status=all adds the history.
func TestHandlerListAllShowsActiveSessionsOfEveryone(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	ctx := context.Background()
	ana := testutil.InsertUser(t, tx, "")
	beto := testutil.InsertUser(t, tx, "")

	if _, err := service.Issue(ctx, ana.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := service.Issue(ctx, beto.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := service.Issue(ctx, ana.ID, time.Hour); err != nil { // to be revoked
		t.Fatalf("issue: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, 'already-expired', now() - interval '1 hour')`, beto.ID); err != nil {
		t.Fatalf("insert an expired token: %v", err) // already expired
	}
	anaTokens, err := service.ListForUser(ctx, ana.ID)
	if err != nil || len(anaTokens) != 2 {
		t.Fatalf("expected 2 tokens for ana, got %d (%v)", len(anaTokens), err)
	}
	if err := service.Revoke(ctx, ana.ID, anaTokens[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	list := func(query string) []tokens.Token {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, "/api/v1/tokens"+query, adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /tokens%s: expected 200, got %d: %s", query, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "hash") {
			t.Errorf("expected no token hash in the response, got %s", rec.Body.String())
		}
		var got []tokens.Token
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}

	active := list("")
	logins := map[string]int{}
	for _, tok := range active {
		logins[tok.UserLogin]++
		if tok.RevokedAt != nil || !tok.ExpiresAt.After(time.Now()) {
			t.Errorf("expected only active tokens by default, got %+v", tok)
		}
	}
	if logins[ana.Login] != 1 || logins[beto.Login] != 1 {
		t.Errorf("expected one active session each for ana and beto, got %v", logins)
	}
	if all := list("?status=all"); len(all) != len(active)+2 {
		t.Errorf("expected ?status=all to add the revoked and the expired one (%d + 2), got %d", len(active), len(all))
	}
	if page := list("?limit=1"); len(page) != 1 {
		t.Errorf("expected limit=1 to give one token, got %d", len(page))
	}
	if rest := list("?limit=1&offset=1"); len(rest) != 1 || rest[0].ID == list("?limit=1")[0].ID {
		t.Errorf("expected offset=1 to give a different token, got %+v", rest)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/tokens?status=bogus", adminToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an unknown status, got %d", rec.Code)
	}
}

func TestHandlerListAllRequiresAdmin(t *testing.T) {
	mux, resolver, _, _ := newHandlerTestServer(t)
	for _, role := range []string{"board", "member"} {
		if rec := doRequest(mux, http.MethodGet, "/api/v1/tokens", testutil.TokenFor(resolver, 1, role), nil); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden, got %d", role, rec.Code)
		}
	}
}

// The session making the request is marked "current" in GET /tokens, so an
// admin can tell which one is theirs (revoking it signs them out).
func TestHandlerListAllMarksTheCallersOwnSession(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	ctx := context.Background()
	other := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(ctx, other.ID, time.Hour); err != nil {
		t.Fatalf("issue: %v", err)
	}

	// The test authenticator hands out its own tokens; give the admin one
	// real row in the tokens table whose hash is that very token's.
	admin := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	if _, err := tx.Exec(ctx, `
		INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, $2, now() + interval '1 hour')
	`, admin.ID, auth.HashToken(adminToken)); err != nil {
		t.Fatalf("insert the caller's token: %v", err)
	}

	rec := doRequest(mux, http.MethodGet, "/api/v1/tokens", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []tokens.Token
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	current := 0
	for _, tok := range list {
		if tok.Current {
			current++
			if tok.UserLogin != admin.Login {
				t.Errorf("the marked session should be the caller's own, got %+v", tok)
			}
		}
	}
	if len(list) < 2 || current != 1 {
		t.Errorf("expected exactly one of %d sessions to be marked current, got %d", len(list), current)
	}
	if strings.Contains(rec.Body.String(), `"current":false`) {
		t.Errorf("expected current to be left out unless true, got %s", rec.Body.String())
	}
}

// The list of sessions carries the browser and system worked out from the last user agent, and
// the user agent itself.
func TestHandlerListAllShowsTheBrowserAndSystemOfEachSession(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	person := testutil.InsertUser(t, tx, "")
	raw, err := service.Issue(ctx, person.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	const ua = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0"
	if _, err := service.Authenticate(ctx, raw, "203.0.113.5", ua); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	never, err := service.Issue(ctx, testutil.InsertUser(t, tx, "").ID, time.Hour) // never used
	if err != nil || never == "" {
		t.Fatalf("issue: %v", err)
	}

	rec := doRequest(mux, http.MethodGet, "/api/v1/tokens", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []tokens.Token
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var used, unused int
	for _, tok := range list {
		switch {
		case tok.UserLogin == person.Login:
			used++
			if tok.Browser != "Firefox 120" || tok.OS != "Linux" || tok.LastUsedUserAgent == nil || *tok.LastUsedUserAgent != ua {
				t.Errorf("unexpected client for the used session: %+v", tok)
			}
		case tok.LastUsedUserAgent == nil:
			unused++
			if tok.Browser != "" || tok.OS != "" {
				t.Errorf("a session never used has no client: %+v", tok)
			}
		}
	}
	if used != 1 || unused < 1 {
		t.Errorf("expected the used session and an unused one in the list, got %d and %d", used, unused)
	}
	if strings.Contains(rec.Body.String(), `"browser":""`) {
		t.Errorf("an unknown browser should be left out, got %s", rec.Body.String())
	}
}

// Everybody sees and ends their own sessions; admin, anybody's; nobody else's.
func TestHandlerAPersonSeesAndRevokesTheirOwnSessions(t *testing.T) {
	mux, resolver, service, tx, _ := newHandlerTestServerWithNotifier(t, nil)
	ctx := context.Background()
	me := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")

	// Two sessions of mine (one expired, one revoked besides) and one of somebody else's.
	activeRaw, _ := service.Issue(ctx, me.ID, time.Hour)
	if _, err := service.Issue(ctx, other.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, 'expired', now() - interval '1 hour')`, me.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, expires_at, revoked_at) VALUES ($1, 'revoked', now() + interval '1 hour', now())`, me.ID); err != nil {
		t.Fatal(err)
	}
	// The one I am calling with is a real row too, so it can be marked.
	myToken := testutil.TokenFor(resolver, me.ID, "member")
	if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, expires_at) VALUES ($1, $2, now() + interval '1 hour')`, me.ID, auth.HashToken(myToken)); err != nil {
		t.Fatal(err)
	}

	own := fmt.Sprintf("/api/v1/users/%s/tokens", me.Login)
	var all, active []tokens.Token
	rec := doRequest(mux, http.MethodGet, own, myToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a person to list their own sessions, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	rec = doRequest(mux, http.MethodGet, own+"?status=active", myToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &active)
	if len(all) != 4 || len(active) != 2 {
		t.Fatalf("expected 4 sessions of which 2 active, got %d and %d", len(all), len(active))
	}
	currents := 0
	for _, tok := range active {
		if tok.UserLogin != me.Login {
			t.Errorf("only my own sessions, got %+v", tok)
		}
		if tok.Current {
			currents++
		}
	}
	if currents != 1 {
		t.Errorf("expected the session in use to be marked, got %d marked", currents)
	}
	expectCode := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Errorf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
		}
	}
	expectCode(doRequest(mux, http.MethodGet, own+"?status=nope", myToken, nil), http.StatusBadRequest)

	// Not anybody else's, and not even to find out whether a login exists.
	expectCode(doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/tokens", other.Login), myToken, nil), http.StatusForbidden)
	expectCode(doRequest(mux, http.MethodGet, "/api/v1/users/nobody.at.all/tokens", myToken, nil), http.StatusForbidden)

	// Ending one of mine; not one of somebody else's; nor changing when one expires.
	var mine tokens.Token
	for _, tok := range active {
		if !tok.Current {
			mine = tok
		}
	}
	otherList, _ := service.ListForUser(ctx, other.ID)
	expectCode(doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", other.Login, otherList[0].ID), myToken, nil), http.StatusForbidden)
	expectCode(doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", me.Login, otherList[0].ID), myToken, nil), http.StatusNotFound)
	expectCode(doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s/tokens/%d", me.Login, mine.ID), myToken,
		map[string]string{"expires_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339)}), http.StatusForbidden)
	expectCode(doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", me.Login, mine.ID), myToken, nil), http.StatusNoContent)
	if _, err := service.Authenticate(ctx, activeRaw, "", ""); err == nil {
		t.Errorf("the session I revoked should no longer work")
	}
	rec = doRequest(mux, http.MethodGet, own+"?status=active", myToken, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &active)
	if len(active) != 1 || !active[0].Current {
		t.Errorf("expected only the session in use to remain, got %+v", active)
	}

	// An admin, anybody's — and a login nobody has is a 404 for them.
	adminToken := testutil.TokenFor(resolver, 999999, "admin")
	expectCode(doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/tokens", other.Login), adminToken, nil), http.StatusOK)
	expectCode(doRequest(mux, http.MethodGet, "/api/v1/users/nobody.at.all/tokens", adminToken, nil), http.StatusNotFound)
}

// Somebody impersonating a person is not that person acting for themselves: they can't see or end
// the person's sessions through it, unless the person is an admin too.
func TestHandlerAnImpersonationSessionCannotManageThePersonsSessions(t *testing.T) {
	mux, resolver, service, tx := newHandlerTestServer(t)
	ctx := context.Background()
	person := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(ctx, person.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	impersonated := testutil.TokenFor(resolver, person.ID, "member")
	resolver[impersonated] = auth.AuthenticatedUser{ID: person.ID, Roles: []string{"member"}, ImpersonatedBy: &admin.ID}

	path := fmt.Sprintf("/api/v1/users/%s/tokens", person.Login)
	if rec := doRequest(mux, http.MethodGet, path, impersonated, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for an impersonation session, got %d", rec.Code)
	}
	list, _ := service.ListForUser(ctx, person.ID)
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("%s/%d", path, list[0].ID), impersonated, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 revoking through an impersonation session, got %d", rec.Code)
	}
}

// Ending one's own session says so in the log channel, in its own words.
func TestHandlerRevokingYourOwnSessionNotifiesInItsOwnWords(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, resolver, service, tx, _ := newHandlerTestServerWithNotifier(t, notifier)
	ctx := context.Background()
	me := testutil.InsertUser(t, tx, "")
	if _, err := service.Issue(ctx, me.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	list, _ := service.ListForUser(ctx, me.ID)
	myToken := testutil.TokenFor(resolver, me.ID, "member")
	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/tokens/%d", me.Login, list[0].ID), myToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, me.Login) || !strings.Contains(msg, "ha cerrado una de sus sesiones") {
		t.Errorf("unexpected notification: %q", msg)
	}
}

// The list of everybody's sessions can be ordered by person, last use, creation and expiry, either way, and
// the order holds across pages (ties break by id, so paging neither repeats nor skips).
func TestHandlerListAllIsSortable(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	ctx := context.Background()
	adminToken := testutil.TokenFor(resolver, testutil.InsertUser(t, tx, "").ID, "admin")

	named := func(first, last string) int64 {
		u := testutil.InsertUser(t, tx, "")
		if _, err := tx.Exec(ctx, `UPDATE users SET first_name = $2, last_name = $3 WHERE id = $1`, u.ID, first, last); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	// Three sessions, by three people, whose dates go in different directions. A never used.
	ana, beto, carla := named("Ana", "Zapata"), named("Beto", "Alonso"), named("Carla", "Moreno")
	row := func(user int64, hash, created, used, expires string) {
		usedSQL := "NULL"
		if used != "" {
			usedSQL = "now() - interval '" + used + "'"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tokens (user_id, token_hash, created_at, last_used_at, expires_at)
			VALUES ($1, $2, now() - interval '`+created+`', `+usedSQL+`, now() + interval '`+expires+`')`, user, hash); err != nil {
			t.Fatal(err)
		}
	}
	row(ana, "s-ana", "3 days", "", "20 days")          // created 3 d ago, never used, ends in 20 d
	row(beto, "s-beto", "2 days", "5 hours", "30 days") // created 2 d ago, used 5 h ago, ends in 30 d
	row(carla, "s-carla", "1 day", "1 hour", "10 days") // created 1 d ago, used 1 h ago, ends in 10 d

	logins := map[int64]string{}
	for _, id := range []int64{ana, beto, carla} {
		l, err := userLogin(tx, id)
		if err != nil {
			t.Fatal(err)
		}
		logins[id] = l
	}
	names := map[string]string{logins[ana]: "Ana", logins[beto]: "Beto", logins[carla]: "Carla"}
	order := func(query string) string {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, "/api/v1/tokens?status=all&"+query, adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", query, rec.Code, rec.Body.String())
		}
		var list []tokens.Token
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tok := range list {
			if n, ok := names[tok.UserLogin]; ok {
				out = append(out, n)
			}
		}
		return strings.Join(out, ",")
	}

	for query, want := range map[string]string{
		"":                           "Carla,Beto,Ana", // the default: last use, most recent first, never used last
		"sort=last_used":             "Carla,Beto,Ana",
		"sort=last_used&order=asc":   "Ana,Beto,Carla", // never used counts as the oldest
		"sort=created":               "Carla,Beto,Ana",
		"sort=created&order=asc":     "Ana,Beto,Carla",
		"sort=expires":               "Beto,Ana,Carla",
		"sort=expires&order=asc":     "Carla,Ana,Beto",
		"sort=person":                "Ana,Beto,Carla", // by name as it is read: first name
		"sort=person&order=desc":     "Carla,Beto,Ana",
		"sort=created&order=desc":    "Carla,Beto,Ana",
		"sort=person&order=asc&x=ok": "Ana,Beto,Carla",
	} {
		if got := order(query); got != want {
			t.Errorf("%q: expected %s, got %s", query, want, got)
		}
	}

	for _, bad := range []string{"sort=name", "sort=LAST_USED", "order=up", "sort=person&order="} {
		rec := doRequest(mux, http.MethodGet, "/api/v1/tokens?"+bad, adminToken, nil)
		wantCode := http.StatusBadRequest
		if bad == "sort=person&order=" { // an empty order is the default, not an error
			wantCode = http.StatusOK
		}
		if rec.Code != wantCode {
			t.Errorf("%q: expected %d, got %d", bad, wantCode, rec.Code)
		}
	}

	// Paging in a given order: every row once, none missing, even though several tie.
	for i := 0; i < 4; i++ { // four more sessions of Ana's, all the same moment
		row(ana, fmt.Sprintf("tie-%d", i), "1 hour", "1 hour", "1 day")
	}
	seen := map[int64]bool{}
	for offset := 0; ; offset += 3 {
		rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/tokens?status=all&sort=person&limit=3&offset=%d", offset), adminToken, nil)
		var page []tokens.Token
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		if len(page) == 0 {
			break
		}
		for _, tok := range page {
			if seen[tok.ID] {
				t.Fatalf("session %d came twice across pages", tok.ID)
			}
			seen[tok.ID] = true
		}
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tokens`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if len(seen) != total {
		t.Errorf("paging should reach every session (%d), reached %d", total, len(seen))
	}
}

func userLogin(tx pgx.Tx, id int64) (string, error) {
	var login string
	err := tx.QueryRow(context.Background(), `SELECT login FROM users WHERE id = $1`, id).Scan(&login)
	return login, err
}

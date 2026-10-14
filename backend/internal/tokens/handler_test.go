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
	if _, err := service.Issue(ctx, beto.ID, -time.Hour); err != nil { // already expired
		t.Fatalf("issue: %v", err)
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

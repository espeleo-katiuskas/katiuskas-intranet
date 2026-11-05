package accounting_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// fakeNotifier stands in for httpapi.MutationNotifier so tests can
// check what a mutation notification actually said, without a real
// Telegram call — same helper board_test/licenses_test etc. each keep
// their own copy of.
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

// waitForCount blocks until at least n messages have been recorded —
// unlike waitForMessage (which only ever waits for the first), this is
// what a test with several setup notifications needs before reset,
// since NotifyAsync's own goroutine for the 2nd (or 3rd...) one can
// still be in flight even after the 1st has already landed.
func (f *fakeNotifier) waitForCount(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) >= n {
			f.mu.Unlock()
			return
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d mutation notifications", n)
}

// reset discards whatever's already been recorded — used between a
// test's setup calls (which fire their own notifications) and the
// actual action under test, so waitForMessage picks up that action's
// own message rather than an earlier setup one. Callers must have
// already waited for every setup notification (waitForMessage/
// waitForCount) before calling this — NotifyAsync is fire-and-forget,
// so clearing sent without waiting first can race a setup call's own
// goroutine still on its way to appending.
func (f *fakeNotifier) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}

// fakeUsers satisfies accounting.UserLookup without needing a real
// users.Service — accounting doesn't key anything by login, so its
// mutation notifications only ever need "who did this", and these
// tests don't care which placeholder actorLogin falls back to when
// the lookup fails.
type fakeUsers struct{}

func (fakeUsers) LoginForUserID(_ context.Context, id int64) (string, error) {
	return "", errors.New("no such user")
}

// payments are the real memberships/licenses services (on the same test
// transaction) behind GET /transactions/{id}'s "dues"/"licenses" — kept
// separate so only the tests that record a cuota or a licencia need them.
type payments struct {
	tx          pgx.Tx
	memberships *memberships.Service
	licenses    *licenses.Service
}

func newHandlerTestServerWithPayments(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *fakeNotifier, payments) {
	t.Helper()
	tx := testutil.NewTx(t)
	service := accounting.NewService(accounting.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	accounting.NewHandler(service, authenticator, fakeUsers{}, membershipsService, licensesService, notifier).RegisterRoutes(mux)
	return mux, resolver, notifier, payments{tx: tx, memberships: membershipsService, licenses: licensesService}
}

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *fakeNotifier) {
	t.Helper()
	mux, resolver, notifier, _ := newHandlerTestServerWithPayments(t)
	return mux, resolver, notifier
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
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body: %s)", err, rec.Body.String())
	}
	return body.Error.Code
}

func createAccount(t *testing.T, mux *http.ServeMux, token, name, code string) accounting.Account {
	t.Helper()
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", token, map[string]string{"name": name, "code": code})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account %s: expected 201, got %d: %s", code, rec.Code, rec.Body.String())
	}
	var a accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	return a
}

// ---- Accounts: reading is open to every baseline role ----

func TestListAccountsOpenToAnyBaselineRole(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/accounts", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list accounts, got %d", role, rec.Code)
		}
	}
}

// ---- Accounts: writing needs admin or the "money" role, not board ----

func TestCreateAccountRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	body := map[string]string{"name": "Caja", "code": "570"}

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating an account, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, body); rec.Code != http.StatusCreated {
		t.Errorf("expected admin to create an account, got %d: %s", rec.Code, rec.Body.String())
	}

	// "money" alone isn't enough — Protect always also requires a
	// baseline role (auth.BaselineRoles) on top of whatever a route
	// specifically lists, the same rule "material" is bound by for
	// material (see docs/architecture.md's "Permissions" section).
	moneyToken := testutil.TokenFor(resolver, 3, "member", "money")
	body2 := map[string]string{"name": "Banco", "code": "572"}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", moneyToken, body2); rec.Code != http.StatusCreated {
		t.Errorf("expected the money role to create an account, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAccountRejectsMissingName(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"code": "570"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 missing_fields, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "missing_fields" {
		t.Errorf("expected missing_fields, got %q", code)
	}
}

// TestCreateAccountAllowsMissingCode and TestCreateAccountAllowsTwoWithNoCode
// cover code actually being optional: omitting it outright succeeds,
// and — since a blank code is normalized to no code at all rather than
// becoming a real, storable "" value (see Handler.normalizeCode) — so
// does sending one that's blank, even for a second account that would
// otherwise collide with the first under accounts.code's own UNIQUE
// constraint.
func TestCreateAccountAllowsMissingCode(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"name": "Caja"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (code is optional), got %d: %s", rec.Code, rec.Body.String())
	}
	var a accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if a.Code != nil {
		t.Errorf("expected a nil code, got %q", *a.Code)
	}
}

func TestCreateAccountAllowsTwoWithNoCode(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"name": "Caja"}); rec.Code != http.StatusCreated {
		t.Fatalf("create 1st account with no code: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"name": "Banco", "code": "   "})
	if rec.Code != http.StatusCreated {
		t.Errorf("expected a 2nd account with a blank code to succeed too, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateAccountRejectsDuplicateCode(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createAccount(t, mux, adminToken, "Caja", "570")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"name": "Otra", "code": "570"})
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 duplicate_code, got %d", rec.Code)
	}
}

func TestHandlerGetAccountNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodGet, "/api/v1/accounts/999999", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// ---- Accounts: an account's own ledger (GET .../transactions) ----

func TestListAccountEntriesOpenToAnyBaselineRole(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")

	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/accounts/"+itoa(caja.ID)+"/transactions", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list account entries, got %d", role, rec.Code)
		}
	}
}

func TestListAccountEntriesNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodGet, "/api/v1/accounts/999999/transactions", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// TestListAccountEntriesShape covers the actual response fields
// through a real HTTP + JSON round trip — repository_test.go covers
// the running-balance/ordering logic itself more thoroughly.
func TestListAccountEntriesShape(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	body := transactionBody(time.Now(), "Cuota", entry(caja.ID, 15), entry(cuotas.ID, -15))
	if rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken, body); rec.Code != http.StatusCreated {
		t.Fatalf("create transaction: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doRequest(mux, http.MethodGet, "/api/v1/accounts/"+itoa(caja.ID)+"/transactions", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []accounting.AccountEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Description != "Cuota" || entries[0].Amount != 15 || entries[0].BalanceAfter != 15 {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestUpdateAccountRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	a := createAccount(t, mux, adminToken, "Caja", "570")

	path := "/api/v1/accounts/" + itoa(a.ID)
	body := map[string]string{"name": "Caja chica", "code": "571"}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPut, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating an account, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPut, path, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Name != "Caja chica" || updated.Code == nil || *updated.Code != "571" {
		t.Errorf("unexpected account after update: %+v", updated)
	}
}

func TestDeleteAccountRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	a := createAccount(t, mux, adminToken, "Caja", "570")
	path := "/api/v1/accounts/" + itoa(a.ID)

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting an account, got %d", rec.Code)
	}

	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestMoveAccountRequiresAdminOrMoneyNotBoard also covers the response
// shape: the whole, freshly reordered GET /accounts list, not just the
// moved account.
func TestMoveAccountRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createAccount(t, mux, adminToken, "Primera", "1")
	b := createAccount(t, mux, adminToken, "Segunda", "2")
	path := "/api/v1/accounts/" + itoa(b.ID) + "/move"

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPost, path, boardToken, map[string]string{"direction": "up"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from moving an account, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"direction": "up"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 || codeOf(list[0]) != "2" || codeOf(list[1]) != "1" {
		t.Errorf("expected [2, 1] after moving Segunda up, got %+v", list)
	}
}

func TestHandlerMoveAccountRejectsInvalidDirection(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	a := createAccount(t, mux, adminToken, "Caja", "570")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts/"+itoa(a.ID)+"/move", adminToken, map[string]string{"direction": "sideways"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_direction, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_direction" {
		t.Errorf("expected invalid_direction, got %q", code)
	}
}

func TestHandlerMoveAccountNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts/999999/move", adminToken, map[string]string{"direction": "up"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCreateAccountNotifiesTelegram(t *testing.T) {
	mux, resolver, notifier := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]string{"name": "Caja", "code": "570"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, "Caja") || !strings.Contains(msg, "570") {
		t.Errorf("expected the notification to name the account, got %q", msg)
	}
}

// The flags are plain bools, always present in the JSON — false is a
// real answer a client can read, not a key that's simply missing.
func TestAccountJSONAlwaysCarriesBothFlags(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_dues": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["allows_dues"] != true || created["allows_licenses"] != false {
		t.Errorf("expected allows_dues true and allows_licenses false, got %v", created)
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/accounts", adminToken, nil)
	var listed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0]["allows_dues"] != true || listed[0]["allows_licenses"] != false {
		t.Errorf("expected the listing to carry both flags, got %v", listed)
	}
}

// PATCH replaces every field, the flags too: leaving one out turns it
// off, the same as leaving out code or parent_id clears those.
func TestUpdateAccountReplacesTheFlagsAndAnOmittedOneMeansFalse(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken,
		map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_dues": true, "allows_licenses": true})
	var a accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := "/api/v1/accounts/" + itoa(a.ID)

	rec = doRequest(mux, http.MethodPut, path, adminToken, map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_licenses": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.AllowsDues || !updated.AllowsLicenses {
		t.Errorf("expected allows_dues off (omitted) and allows_licenses on, got %+v", updated)
	}
}

func TestUpdateAccountNotificationNamesAChangedFlag(t *testing.T) {
	mux, resolver, notifier := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	a := createAccount(t, mux, adminToken, "Cuotas", "700")
	notifier.waitForMessage(t)
	notifier.reset()

	rec := doRequest(mux, http.MethodPut, "/api/v1/accounts/"+itoa(a.ID), adminToken,
		map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_dues": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, "AllowsDues: false → true") {
		t.Errorf("expected the notification to name the flag that changed, got %q", msg)
	}
}

// ---- Transactions: reading is open to every baseline role ----

func TestListTransactionsOpenToAnyBaselineRole(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/transactions", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list transactions, got %d", role, rec.Code)
		}
	}
}

// TestListTransactionsClampsNegativeOffset covers the fix for a
// previously unhandled 500: a negative offset used to reach Postgres's
// own OFFSET clause untouched, which rejects it outright. Now
// defaulted to the start instead, same as an out-of-range limit
// already was.
func TestListTransactionsClampsNegativeOffset(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	if rec := doRequest(mux, http.MethodGet, "/api/v1/transactions?offset=-1", token, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a negative offset to be clamped rather than error, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---- Transactions: writing needs admin or the "money" role, not board ----

func transactionBody(occurredAt time.Time, description string, entries ...map[string]any) map[string]any {
	return map[string]any{
		"description": description,
		"occurred_at": occurredAt.Format(time.RFC3339),
		"entries":     entries,
	}
}

func entry(accountID int64, amount float64) map[string]any {
	return map[string]any{"account_id": accountID, "amount": amount}
}

func TestCreateTransactionRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	body := transactionBody(time.Now(), "Cuota", entry(caja.ID, 10), entry(cuotas.ID, -10))

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a transaction, got %d", rec.Code)
	}

	moneyToken := testutil.TokenFor(resolver, 3, "member", "money")
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", moneyToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected the money role to create a transaction, got %d: %s", rec.Code, rec.Body.String())
	}
	var created accounting.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.Entries) != 2 {
		t.Errorf("expected 2 entries, got %+v", created.Entries)
	}
}

func TestHandlerCreateTransactionRejectsUnbalancedEntries(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	body := transactionBody(time.Now(), "Desbalanceada", entry(caja.ID, 10), entry(cuotas.ID, -5))
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken, body)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 unbalanced, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unbalanced" {
		t.Errorf("expected error code unbalanced, got %q", code)
	}
}

func TestHandlerCreateTransactionRejectsTooFewEntries(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")

	body := transactionBody(time.Now(), "Una entrada", entry(caja.ID, 10))
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 too_few_entries, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "too_few_entries" {
		t.Errorf("expected error code too_few_entries, got %q", code)
	}
}

func TestCreateTransactionRejectsMalformedTimestamp(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	body := map[string]any{
		"description": "Fecha mala",
		"occurred_at": "not-a-timestamp",
		"entries":     []map[string]any{entry(caja.ID, 10), entry(cuotas.ID, -10)},
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerGetTransactionNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodGet, "/api/v1/transactions/999999", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateTransactionRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	createRec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "Original", entry(caja.ID, 10), entry(cuotas.ID, -10)))
	var created accounting.Transaction
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := "/api/v1/transactions/" + itoa(created.ID)
	body := transactionBody(created.OccurredAt, "Corregida", entry(caja.ID, 20), entry(cuotas.ID, -20))

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPut, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a transaction, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPut, path, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated accounting.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Description != "Corregida" || updated.Entries[0].Amount != 20 {
		t.Errorf("unexpected transaction after update: %+v", updated)
	}
}

func TestDeleteTransactionRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	createRec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "A borrar", entry(caja.ID, 10), entry(cuotas.ID, -10)))
	var created accounting.Transaction
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := "/api/v1/transactions/" + itoa(created.ID)

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting a transaction, got %d", rec.Code)
	}

	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateTransactionNotifiesTelegram also covers positiveEntriesTotal
// directly: the notification names the transaction's own amount (the
// sum of only its positive entries — the actual amount of money moved,
// since every transaction's entries always sum to zero on their own),
// not an entry count or anything that would just read as zero.
func TestCreateTransactionNotifiesTelegram(t *testing.T) {
	mux, resolver, notifier := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")
	notifier.waitForCount(t, 2) // both account-create notifications above
	notifier.reset()

	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "Cuota mensual", entry(caja.ID, 10), entry(cuotas.ID, -10)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, "ha creado la transacción «Cuota mensual» (10.00€)") {
		t.Errorf("expected the notification to name the transaction and its amount, got %q", msg)
	}
}

// TestCreateTransactionNotifiesTelegramSumsEveryPositiveEntry covers
// positiveEntriesTotal with more than one entry on the positive side —
// a two-entry transaction alone couldn't tell "sums every positive
// entry" apart from a bug that just took the first one.
func TestCreateTransactionNotifiesTelegramSumsEveryPositiveEntry(t *testing.T) {
	mux, resolver, notifier := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	banco := createAccount(t, mux, adminToken, "Banco", "572")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")
	notifier.waitForCount(t, 3) // the three account-create notifications above
	notifier.reset()

	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "Reparto de cuota", entry(caja.ID, 15), entry(banco.ID, 10), entry(cuotas.ID, -25)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, "(25.00€)") {
		t.Errorf("expected the notification to sum both positive entries (15 + 10), got %q", msg)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// GET /transactions/{id} also says which cuotas and licencias the
// transaction paid for — but only to admin/board: who paid which is
// private to that person, admin and board (GET /users/{login}/dues),
// while a transaction itself is open to every baseline role.
func TestGetTransactionIncludesWhatItPaidForOnlyForAdminAndBoard(t *testing.T) {
	mux, resolver, _, p := newHandlerTestServerWithPayments(t)
	ctx := context.Background()
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	cuotas := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken,
		map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_dues": true, "allows_licenses": true})
	var cuotasAccount accounting.Account
	if err := json.Unmarshal(cuotas.Body.Bytes(), &cuotasAccount); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "Cuota y licencia", entry(caja.ID, 100), entry(cuotasAccount.ID, -100)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transaction: %d: %s", rec.Code, rec.Body.String())
	}
	var created accounting.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}

	person := testutil.InsertUser(t, p.tx, "")
	if _, err := p.memberships.RecordDue(ctx, person.ID, 2026, created.ID, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record due: %v", err)
	}
	if _, err := p.licenses.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: testutil.LicenseTypeID(t, p.tx, "A BASICA"),
		IssuedDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Year: 2026, TransactionID: &created.ID,
	}); err != nil {
		t.Fatalf("create license: %v", err)
	}
	path := "/api/v1/transactions/" + itoa(created.ID)

	for _, role := range []string{"admin", "board"} {
		rec := doRequest(mux, http.MethodGet, path, testutil.TokenFor(resolver, 2, role), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", role, rec.Code, rec.Body.String())
		}
		var got struct {
			Dues     []map[string]any `json:"dues"`
			Licenses []map[string]any `json:"licenses"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Dues) != 1 || got.Dues[0]["user_login"] != person.Login || got.Dues[0]["year"] != float64(2026) {
			t.Errorf("%s: expected the one due, with its person's login, got %v", role, got.Dues)
		}
		if len(got.Licenses) != 1 || got.Licenses[0]["user_login"] != person.Login || got.Licenses[0]["type"] != "A BASICA" {
			t.Errorf("%s: expected the one license, with its owner's login, got %v", role, got.Licenses)
		}
	}

	rec = doRequest(mux, http.MethodGet, path, testutil.TokenFor(resolver, person.ID, "member"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a plain member to still read the transaction, got %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, `"dues"`) || strings.Contains(body, `"licenses"`) || strings.Contains(body, person.Login) {
		t.Errorf("expected nothing about who paid for what to reach a plain member, got %s", body)
	}
}

// For admin/board the two lists are always there — empty, not absent,
// when the transaction paid for none — so a client can tell "none" from
// "not allowed to see".
func TestGetTransactionListsAreEmptyNotAbsentForAdminWhenItPaidForNothing(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	banco := createAccount(t, mux, adminToken, "Banco", "572")
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
		transactionBody(time.Now(), "Traspaso", entry(caja.ID, 5), entry(banco.ID, -5)))
	var created accounting.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/transactions/"+itoa(created.ID), adminToken, nil)
	body := rec.Body.String()
	if !strings.Contains(body, `"dues":[]`) || !strings.Contains(body, `"licenses":[]`) {
		t.Errorf("expected empty dues and licenses arrays, got %s", body)
	}
}

func TestHandlerCreateTransactionRejectsDuplicateAccount(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	cuotas := createAccount(t, mux, adminToken, "Cuotas", "700")

	body := transactionBody(time.Now(), "Repetida", entry(caja.ID, 10), entry(caja.ID, 5), entry(cuotas.ID, -15))
	rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 duplicate_account, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "duplicate_account" {
		t.Errorf("expected error code duplicate_account, got %q", code)
	}
}

func TestListTransactionsRejectsBadDates(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, token, "Caja", "570")
	for _, query := range []string{"from=ayer", "to=2024-13-01", "from=2024-05-02&to=2024-05-01"} {
		for _, path := range []string{"/api/v1/transactions?", fmt.Sprintf("/api/v1/accounts/%d/transactions?", caja.ID)} {
			rec := doRequest(mux, http.MethodGet, path+query, token, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s%s: expected 400, got %d: %s", path, query, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestHandlerAccountHiddenFlag(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": "Antigua", "code": "999", "hidden": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || !created.Hidden {
		t.Fatalf("expected a hidden account, got %+v (%v)", created, err)
	}
	if !strings.Contains(rec.Body.String(), `"hidden":true`) {
		t.Errorf("expected hidden in the JSON, got %s", rec.Body.String())
	}
	// Still listed: hiding is a presentation matter, the API never filters.
	list := doRequest(mux, http.MethodGet, "/api/v1/accounts", adminToken, nil)
	if !strings.Contains(list.Body.String(), `"hidden":true`) {
		t.Errorf("expected the hidden account in the listing, got %s", list.Body.String())
	}
}

// A license outlives the record of how it was paid (licenses.transaction_id
// is ON DELETE RESTRICT), so deleting its transaction is a 409 — not the
// 500 the foreign-key error used to become — and the transaction stays. A
// transaction that only paid dues is deleted, dues included.
func TestDeleteTransactionHandlesLicensesAndDues(t *testing.T) {
	mux, resolver, _, p := newHandlerTestServerWithPayments(t)
	ctx := context.Background()
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	cuotas := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken,
		map[string]any{"name": "Cuotas", "code": "700", "type": "income", "allows_dues": true, "allows_licenses": true})
	var cuotasAccount accounting.Account
	if err := json.Unmarshal(cuotas.Body.Bytes(), &cuotasAccount); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	caja := createAccount(t, mux, adminToken, "Caja", "570")
	newTransaction := func(description string) accounting.Transaction {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", adminToken,
			transactionBody(time.Now(), description, entry(caja.ID, 50), entry(cuotasAccount.ID, -50)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create transaction: %d: %s", rec.Code, rec.Body.String())
		}
		var created accounting.Transaction
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode transaction: %v", err)
		}
		return created
	}
	person := testutil.InsertUser(t, p.tx, "")
	paidOn := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	withLicense := newTransaction("Cuota y licencia")
	if _, err := p.memberships.RecordDue(ctx, person.ID, 2026, withLicense.ID, paidOn); err != nil {
		t.Fatalf("record due: %v", err)
	}
	if _, err := p.licenses.Create(ctx, licenses.CreateInput{
		UserID: person.ID, LicenseTypeID: testutil.LicenseTypeID(t, p.tx, "A BASICA"),
		IssuedDate: paidOn, Year: 2026, TransactionID: &withLicense.ID,
	}); err != nil {
		t.Fatalf("create license: %v", err)
	}
	rec := doRequest(mux, http.MethodDelete, "/api/v1/transactions/"+itoa(withLicense.ID), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 transaction_has_licenses, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "transaction_has_licenses") {
		t.Errorf("expected error code transaction_has_licenses, got %s", rec.Body.String())
	}
	if got := doRequest(mux, http.MethodGet, "/api/v1/transactions/"+itoa(withLicense.ID), adminToken, nil); got.Code != http.StatusOK {
		t.Errorf("expected the transaction to still exist, got %d", got.Code)
	}

	onlyDue := newTransaction("Solo cuota")
	if _, err := p.memberships.RecordDue(ctx, person.ID, 2027, onlyDue.ID, paidOn.AddDate(1, 0, 0)); err != nil {
		t.Fatalf("record due: %v", err)
	}
	if rec := doRequest(mux, http.MethodDelete, "/api/v1/transactions/"+itoa(onlyDue.ID), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for a transaction that only paid a due, got %d: %s", rec.Code, rec.Body.String())
	}
	dues, err := p.memberships.ListDuesForUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list dues: %v", err)
	}
	if len(dues) != 1 || dues[0].Year != 2026 {
		t.Errorf("expected only the 2026 due to remain, got %+v", dues)
	}
}

// An account's type is optional, one of asset, income or expense, listed and
// returned with the account; a blank one is no type; PATCH replaces it like
// every other field, so leaving it out clears it.
func TestAccountTypeIsOptionalValidatedAndReplacedByUpdate(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	decodeAccount := func(rec *httptest.ResponseRecorder) accounting.Account {
		t.Helper()
		var a accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return a
	}
	typeOf := func(a accounting.Account) string {
		if a.Type == nil {
			return ""
		}
		return string(*a.Type)
	}

	// Each of the three kinds, none, and a blank one (the same as none).
	created := map[string]accounting.Account{}
	for _, c := range []struct{ name, kind string }{{"Caja", "asset"}, {"Cuotas", "income"}, {"Material", "expense"}, {"Agrupación", ""}} {
		body := map[string]any{"name": c.name}
		if c.kind != "" {
			body["type"] = c.kind
		}
		rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for %q, got %d: %s", c.name, rec.Code, rec.Body.String())
		}
		a := decodeAccount(rec)
		if typeOf(a) != c.kind {
			t.Errorf("%s: expected type %q, got %q", c.name, c.kind, typeOf(a))
		}
		created[c.name] = a
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": "En blanco", "type": "  "}); rec.Code != http.StatusCreated || typeOf(decodeAccount(rec)) != "" {
		t.Errorf("a blank type should be none: %d %s", rec.Code, rec.Body.String())
	}

	// A type that isn't one is refused, on creating and on editing.
	for _, bad := range []string{"liability", "ASSET", "x"} {
		rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": "Mala", "type": bad})
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_type" {
			t.Errorf("%q on create: expected 400 invalid_type, got %d %s", bad, rec.Code, rec.Body.String())
		}
		path := "/api/v1/accounts/" + itoa(created["Caja"].ID)
		rec = doRequest(mux, http.MethodPut, path, adminToken, map[string]any{"name": "Caja", "type": bad})
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_type" {
			t.Errorf("%q on update: expected 400 invalid_type, got %d %s", bad, rec.Code, rec.Body.String())
		}
	}

	// The list carries them, and so does GET.
	rec := doRequest(mux, http.MethodGet, "/api/v1/accounts", adminToken, nil)
	var list []accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	got := map[string]string{}
	for _, a := range list {
		got[a.Name] = typeOf(a)
	}
	if got["Caja"] != "asset" || got["Cuotas"] != "income" || got["Material"] != "expense" || got["Agrupación"] != "" {
		t.Errorf("unexpected types in the list: %v", got)
	}
	if a := decodeAccount(doRequest(mux, http.MethodGet, "/api/v1/accounts/"+itoa(created["Cuotas"].ID), adminToken, nil)); typeOf(a) != "income" {
		t.Errorf("GET should carry the type: %+v", a)
	}

	// An edit replaces it: another one, and none when it is left out.
	path := "/api/v1/accounts/" + itoa(created["Caja"].ID)
	if a := decodeAccount(doRequest(mux, http.MethodPut, path, adminToken, map[string]any{"name": "Caja", "type": "expense"})); typeOf(a) != "expense" {
		t.Errorf("expected the type to change: %+v", a)
	}
	if a := decodeAccount(doRequest(mux, http.MethodPut, path, adminToken, map[string]any{"name": "Caja"})); a.Type != nil {
		t.Errorf("leaving it out should clear it: %+v", a)
	}

	// Moving an account answers with the list, types included.
	moved := doRequest(mux, http.MethodPost, "/api/v1/accounts/"+itoa(created["Cuotas"].ID)+"/move", adminToken, map[string]string{"direction": "up"})
	if moved.Code != http.StatusOK || !strings.Contains(moved.Body.String(), `"type":"income"`) {
		t.Errorf("moving should keep the types: %d %s", moved.Code, moved.Body.String())
	}
}

// Only one account is the default for the payments of distributions: marking
// another takes the mark off the first, however it is done (create or edit), and
// editing without the mark turns it off.
func TestHandlerDistributionDefaultAccountIsUnique(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	create := func(name string, isDefault bool) accounting.Account {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": name, "type": "income", "distribution_default": isDefault})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d: %s", name, rec.Code, rec.Body.String())
		}
		var a accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	defaults := func() []string {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, "/api/v1/accounts", adminToken, nil)
		var all []accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, a := range all {
			if a.DistributionDefault {
				names = append(names, a.Name)
			}
		}
		return names
	}
	first := create("Repartos", true)
	if got := defaults(); len(got) != 1 || got[0] != "Repartos" {
		t.Fatalf("expected Repartos as default, got %v", got)
	}
	create("Lotería", true)
	if got := defaults(); len(got) != 1 || got[0] != "Lotería" {
		t.Fatalf("creating another default should replace it, got %v", got)
	}
	rec := doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", first.ID), adminToken, map[string]any{"name": "Repartos", "type": "income", "distribution_default": true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"distribution_default":true`) {
		t.Fatalf("edit: %d: %s", rec.Code, rec.Body.String())
	}
	if got := defaults(); len(got) != 1 || got[0] != "Repartos" {
		t.Fatalf("editing should move the mark, got %v", got)
	}
	// Only an income account can be the default, or lose that type while it is.
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", first.ID), adminToken, map[string]any{"name": "Repartos", "type": "asset", "distribution_default": true})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_income_for_distributions") {
		t.Fatalf("expected 409 not_income_for_distributions, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", adminToken, map[string]any{"name": "Caja", "type": "asset", "distribution_default": true}); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 creating an asset default, got %d: %s", rec.Code, rec.Body.String())
	}
	// Editing without it turns it off (PATCH replaces every field).
	doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", first.ID), adminToken, map[string]any{"name": "Repartos"})
	if got := defaults(); len(got) != 0 {
		t.Fatalf("expected no default, got %v", got)
	}
}

// q narrows both listings to the transactions whose description has every word,
// in any order, ignoring case and accents; the running balance of the ledger
// stays what it is without the search.
func TestListTransactionsSearchesWordsInTheDescription(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	caja := createAccount(t, mux, token, "Caja", "570")
	otra := createAccount(t, mux, token, "Otra", "571")
	now := time.Now()
	for i, description := range []string{"Pago de cuota de Álvaro Núñez", "Compra de cuerdas", "Cuota de María 100%"} {
		rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", token,
			transactionBody(now.Add(time.Duration(i)*time.Minute), description, entry(caja.ID, 10), entry(otra.ID, -10)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
		}
	}
	descriptions := func(path, query string) []string {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, path+"?q="+url.QueryEscape(query), token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s?q=%s: %d: %s", path, query, rec.Code, rec.Body.String())
		}
		var list []struct {
			Description  string  `json:"description"`
			BalanceAfter float64 `json:"balance_after"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range list {
			out = append(out, e.Description)
		}
		return out
	}
	ledger := fmt.Sprintf("/api/v1/accounts/%d/transactions", caja.ID)
	for _, path := range []string{"/api/v1/transactions", ledger} {
		if got := descriptions(path, ""); len(got) != 3 {
			t.Errorf("%s without q: %v", path, got)
		}
		if got := descriptions(path, "CUOTA"); len(got) != 2 {
			t.Errorf("%s case: %v", path, got)
		}
		if got := descriptions(path, "alvaro nunez cuota"); len(got) != 1 || got[0] != "Pago de cuota de Álvaro Núñez" {
			t.Errorf("%s accents and order: %v", path, got)
		}
		if got := descriptions(path, "cuota cuerdas"); len(got) != 0 {
			t.Errorf("%s every word must be there: %v", path, got)
		}
		if got := descriptions(path, "100%"); len(got) != 1 {
			t.Errorf("%s a percent sign is just a character: %v", path, got)
		}
		if got := descriptions(path, "_"); len(got) != 0 {
			t.Errorf("%s an underscore is just a character: %v", path, got)
		}
	}
	// The balance after a transaction doesn't depend on the search.
	rec := doRequest(mux, http.MethodGet, ledger+"?q=cuerdas", token, nil)
	if !strings.Contains(rec.Body.String(), `"balance_after":20`) {
		t.Errorf("expected the balance after the second transaction, got %s", rec.Body.String())
	}
}

// An account either groups other accounts or has transactions, never both: no
// entry can be posted to one with children, and one with entries can't be given
// as a parent, when creating or editing.
func TestAccountsEitherHaveChildrenOrTransactions(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	bancos := createAccount(t, mux, token, "Bancos", "")
	banco := doRequest(mux, http.MethodPost, "/api/v1/accounts", token, map[string]any{"name": "Banco", "parent_id": bancos.ID})
	if banco.Code != http.StatusCreated {
		t.Fatalf("create child: %d: %s", banco.Code, banco.Body.String())
	}
	var child accounting.Account
	if err := json.Unmarshal(banco.Body.Bytes(), &child); err != nil {
		t.Fatal(err)
	}
	caja := createAccount(t, mux, token, "Caja", "")
	post := func(accounts ...int64) *httptest.ResponseRecorder {
		return doRequest(mux, http.MethodPost, "/api/v1/transactions", token,
			transactionBody(time.Now(), "Movimiento", entry(accounts[0], 5), entry(accounts[1], -5)))
	}
	// The group can't be on a transaction, create or edit; its child can.
	rec := post(bancos.ID, caja.ID)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "account_has_children" {
		t.Fatalf("expected 409 account_has_children, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = post(child.ID, caja.ID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a leaf can have transactions: %d: %s", rec.Code, rec.Body.String())
	}
	var created accounting.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rec = doRequest(mux, http.MethodPut, "/api/v1/transactions/"+itoa(created.ID), token,
		transactionBody(time.Now(), "Movimiento", entry(bancos.ID, 5), entry(caja.ID, -5)))
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "account_has_children" {
		t.Fatalf("expected 409 editing a transaction onto a group, got %d: %s", rec.Code, rec.Body.String())
	}
	// Caja has entries now, so it can't become a parent.
	rec = doRequest(mux, http.MethodPost, "/api/v1/accounts", token, map[string]any{"name": "Hija de caja", "parent_id": caja.ID})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "parent_has_entries" {
		t.Fatalf("expected 409 parent_has_entries creating, got %d: %s", rec.Code, rec.Body.String())
	}
	other := createAccount(t, mux, token, "Otra", "")
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", other.ID), token, map[string]any{"name": "Otra", "parent_id": caja.ID})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "parent_has_entries" {
		t.Fatalf("expected 409 parent_has_entries editing, got %d: %s", rec.Code, rec.Body.String())
	}
	// An account with no entries can still be moved under a group.
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", other.ID), token, map[string]any{"name": "Otra", "parent_id": bancos.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 moving under a group, got %d: %s", rec.Code, rec.Body.String())
	}
}

// An account with no type of its own has its parent's, recursively: the listing
// says which in effective_type, and the rules about distributions' accounts
// follow it, also when a parent's type changes under them.
func TestAccountsWithoutTypeInheritTheirParents(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	create := func(body map[string]any) accounting.Account {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", token, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %v: %d: %s", body, rec.Code, rec.Body.String())
		}
		var a accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	effective := func(id int64) string {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, "/api/v1/accounts", token, nil)
		var all []accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
			t.Fatal(err)
		}
		for _, a := range all {
			if a.ID == id {
				if a.EffectiveType == nil {
					return ""
				}
				return string(*a.EffectiveType)
			}
		}
		t.Fatalf("account %d not listed", id)
		return ""
	}
	root := create(map[string]any{"name": "Ingresos", "type": "income"})
	middle := create(map[string]any{"name": "Cuotas", "parent_id": root.ID})
	leaf := create(map[string]any{"name": "Lotería", "parent_id": middle.ID, "distribution_default": true})
	if leaf.EffectiveType == nil || *leaf.EffectiveType != accounting.AccountTypeIncome || leaf.Type != nil {
		t.Fatalf("a created account says its effective type: %+v", leaf)
	}
	if got := effective(leaf.ID); got != "income" {
		t.Fatalf("expected income inherited through two levels, got %q", got)
	}
	own := create(map[string]any{"name": "Gastos", "type": "expense", "parent_id": root.ID})
	if got := effective(own.ID); got != "expense" {
		t.Fatalf("an own type wins, got %q", got)
	}
	// A default account has to be income, inherited or not.
	rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", token, map[string]any{"name": "Mal", "parent_id": own.ID, "distribution_default": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a default inheriting expense, got %d: %s", rec.Code, rec.Body.String())
	}
	// Changing the type of a parent changes the whole subtree, which is refused while
	// the default account is under it.
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", root.ID), token, map[string]any{"name": "Ingresos", "type": "asset"})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_income_for_distributions" {
		t.Fatalf("expected 409 changing the type above the default, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := effective(leaf.ID); got != "income" {
		t.Fatalf("the refused change must not stick, got %q", got)
	}
	// Moving the default account under an expense parent is refused too.
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", leaf.ID), token, map[string]any{"name": "Lotería", "parent_id": own.ID, "distribution_default": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 moving the default under an expense, got %d: %s", rec.Code, rec.Body.String())
	}
	// A parent with no type anywhere above gives none.
	loose := create(map[string]any{"name": "Suelta"})
	child := create(map[string]any{"name": "Hija", "parent_id": loose.ID})
	if effective(child.ID) != "" {
		t.Fatalf("no type anywhere should give none")
	}
}

// The ledger of an account that groups others lists the entries of all its
// descendants, oldest balance first, each with the account it is on, and the
// running balance is that of all of them together.
func TestLedgerOfAGroupListsItsDescendantsWithATotalBalance(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	bancos := createAccount(t, mux, token, "Bancos", "")
	child := func(name string, parent int64) accounting.Account {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/accounts", token, map[string]any{"name": name, "parent_id": parent})
		var a accounting.Account
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body.String())
		}
		return a
	}
	santander := child("Santander", bancos.ID)
	corriente := child("Corriente", santander.ID)
	bbva := child("BBVA", bancos.ID)
	ingresos := createAccount(t, mux, token, "Ingresos", "")
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	post := func(offset int, description string, account int64, amount float64, other int64) {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", token,
			transactionBody(day.AddDate(0, 0, offset), description, entry(account, amount), entry(other, -amount)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("post %s: %d %s", description, rec.Code, rec.Body.String())
		}
	}
	post(0, "Cuota", corriente.ID, 100, ingresos.ID)
	post(1, "Otra cuota", bbva.ID, 50, ingresos.ID)
	post(2, "Traspaso", bbva.ID, 30, corriente.ID) // both inside the group: two rows
	rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/transactions", bancos.ID), token, nil)
	var got []accounting.AccountEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	// Newest first: the transfer's two rows, then the other two.
	type row struct {
		account string
		amount  float64
		balance float64
	}
	var rows []row
	for _, e := range got {
		rows = append(rows, row{e.AccountName, e.Amount, e.BalanceAfter})
	}
	// The total is that of the whole group up to each row, not of its account: the
	// transfer's two rows leave it at 180 and then 150 (its entries are in the order
	// they were written, listed newest first).
	want := []row{{"Corriente", -30, 150}, {"BBVA", 30, 180}, {"BBVA", 50, 150}, {"Corriente", 100, 100}}
	if len(rows) != 4 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d: expected %+v, got %+v", i, want[i], rows[i])
		}
	}
	// A leaf still lists only its own entries, with its own balance.
	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/transactions", bbva.ID), token, nil)
	got = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 || got[0].BalanceAfter != 80 || got[0].AccountName != "BBVA" {
		t.Fatalf("unexpected leaf ledger: %v %+v", err, got)
	}
}

// An account marked for annual dues or licenses has to be of type income, its own
// or inherited. Only the account being saved and what is below it are checked, so
// accounts that predate the rule don't block unrelated edits.
func TestAccountsForDuesAndLicensesHaveToBeIncome(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	token := testutil.TokenFor(resolver, 1, "admin")
	create := func(body map[string]any) *httptest.ResponseRecorder {
		return doRequest(mux, http.MethodPost, "/api/v1/accounts", token, body)
	}
	for _, flag := range []string{"allows_dues", "allows_licenses"} {
		rec := create(map[string]any{"name": "Sin tipo " + flag, flag: true})
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_income_for_dues_licenses" {
			t.Fatalf("%s with no type: expected 409, got %d: %s", flag, rec.Code, rec.Body.String())
		}
		rec = create(map[string]any{"name": "Gastos " + flag, "type": "expense", flag: true})
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s as expense: expected 409, got %d: %s", flag, rec.Code, rec.Body.String())
		}
	}
	rec := create(map[string]any{"name": "Ingresos", "type": "income"})
	var root accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create root: %d %s", rec.Code, rec.Body.String())
	}
	// Inheriting income is enough.
	rec = create(map[string]any{"name": "Cuotas", "parent_id": root.ID, "allows_dues": true})
	var cuotas accounting.Account
	if err := json.Unmarshal(rec.Body.Bytes(), &cuotas); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 inheriting income, got %d: %s", rec.Code, rec.Body.String())
	}
	// The type above can't change out from under it.
	rec = doRequest(mux, http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d", root.ID), token, map[string]any{"name": "Ingresos", "type": "asset"})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_income_for_dues_licenses" {
		t.Fatalf("expected 409 changing the type above it, got %d: %s", rec.Code, rec.Body.String())
	}
	if cuotas.EffectiveType == nil || *cuotas.EffectiveType != accounting.AccountTypeIncome {
		t.Errorf("expected the inherited income type, got %+v", cuotas.EffectiveType)
	}
}

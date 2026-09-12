package accounting_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
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

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *fakeNotifier) {
	t.Helper()
	tx := testutil.NewTx(t)
	service := accounting.NewService(accounting.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	accounting.NewHandler(service, authenticator, fakeUsers{}, notifier).RegisterRoutes(mux)
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
	// inventory (see CLAUDE.md's "Baseline access policy").
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

func TestUpdateAccountRequiresAdminOrMoneyNotBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	a := createAccount(t, mux, adminToken, "Caja", "570")

	path := "/api/v1/accounts/" + itoa(a.ID)
	body := map[string]string{"name": "Caja chica", "code": "571"}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPatch, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating an account, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPatch, path, adminToken, body)
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
	if rec := doRequest(mux, http.MethodPatch, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a transaction, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPatch, path, adminToken, body)
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
	if !strings.Contains(msg, "Cuota mensual") {
		t.Errorf("expected the notification to name the transaction, got %q", msg)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

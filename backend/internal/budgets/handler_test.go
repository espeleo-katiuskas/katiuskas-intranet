package budgets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/budgets"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

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

func (f *fakeNotifier) wait(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.sent = f.sent[1:]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a notification")
	return ""
}

type env struct {
	tx       pgx.Tx
	mux      *http.ServeMux
	notifier *fakeNotifier
	admin    string
	money    string
	board    string
	member   string
	// Leaf accounts: two of income, two of expense, one asset.
	sales, fees, rent, gear, bank int64
}

func (e *env) account(t *testing.T, name, typ string, parent *int64) int64 {
	t.Helper()
	var id int64
	var typeArg any
	if typ != "" {
		typeArg = typ
	}
	err := e.tx.QueryRow(context.Background(),
		`INSERT INTO accounts (name, type, parent_id, position) VALUES ($1, $2::account_type, $3, 1) RETURNING id`,
		name, typeArg, parent).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// post records money moving from one account to another on the given day.
func (e *env) post(t *testing.T, from, to int64, amount float64, day string) {
	t.Helper()
	var id int64
	if err := e.tx.QueryRow(context.Background(),
		`INSERT INTO transactions (description, occurred_at) VALUES ('t', $1::timestamptz) RETURNING id`, day+" 12:00:00+00").Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, en := range []struct {
		acc int64
		amt float64
	}{{from, -amount}, {to, amount}} {
		if _, err := e.tx.Exec(context.Background(),
			`INSERT INTO transaction_entries (transaction_id, account_id, amount) VALUES ($1, $2, $3)`, id, en.acc, en.amt); err != nil {
			t.Fatal(err)
		}
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	// The seeded ledger would have to be covered by every budget these tests save.
	if _, err := tx.Exec(context.Background(), `UPDATE accounts SET type = NULL`); err != nil {
		t.Fatal(err)
	}
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	budgets.NewHandler(budgets.NewService(budgets.NewRepository(tx)), authenticator, usersService, notifier).RegisterRoutes(mux)
	admin := testutil.InsertUser(t, tx, "")
	money := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	e := &env{
		tx: tx, mux: mux, notifier: notifier,
		admin:  testutil.TokenFor(resolver, admin.ID, "admin"),
		money:  testutil.TokenFor(resolver, money.ID, "member", "money"),
		board:  testutil.TokenFor(resolver, board.ID, "board"),
		member: testutil.TokenFor(resolver, member.ID, "member"),
	}
	e.sales = e.account(t, "ZZ Sales", "income", nil)
	e.fees = e.account(t, "ZZ Fees", "income", nil)
	e.rent = e.account(t, "ZZ Rent", "expense", nil)
	e.gear = e.account(t, "ZZ Gear", "expense", nil)
	e.bank = e.account(t, "ZZ Bank", "asset", nil)
	return e
}

func (e *env) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	if code != "" {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != code {
			t.Fatalf("expected error code %q, got %q (%s)", code, body.Error.Code, rec.Body.String())
		}
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

type item = map[string]any

func (e *env) validBody() map[string]any {
	return map[string]any{
		"income": []item{
			{"name": "Sales", "amount": 1000, "account_ids": []int64{e.sales}},
			{"name": "Fees", "amount": 500.5, "account_ids": []int64{e.fees}},
		},
		"expenses": []item{
			{"name": "Premises", "amount": 1200.5, "account_ids": []int64{e.rent}},
			{"name": "Gear", "amount": 300, "account_ids": []int64{e.gear}},
		},
	}
}

func TestSaveAndGet(t *testing.T) {
	e := newEnv(t)
	e.post(t, e.sales, e.bank, 400, "2031-03-10")   // income of 2031
	e.post(t, e.fees, e.bank, 100, "2031-12-31")    // last day of 2031
	e.post(t, e.fees, e.bank, 999, "2032-01-01")    // next year, not counted
	e.post(t, e.bank, e.rent, 600.25, "2031-01-01") // first day, expense
	e.post(t, e.bank, e.gear, 50, "2030-12-31")     // previous year

	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.money, e.validBody()), http.StatusOK, "")
	rec := e.do("GET", "/api/v1/budgets/2031", e.member, nil)
	expect(t, rec, http.StatusOK, "")
	b := decode[budgets.Budget](t, rec)
	if b.Income.Amount != 1500.5 || b.Income.Actual != 500 || b.Expenses.Amount != 1500.5 || b.Expenses.Actual != 600.25 {
		t.Fatalf("totals: %+v / %+v", b.Income, b.Expenses)
	}
	if b.Income.Percent == nil || *b.Income.Percent != 33.32 {
		t.Fatalf("income percent: %v", b.Income.Percent)
	}
	if len(b.Income.Items) != 2 || b.Income.Items[0].Name != "Sales" || *b.Income.Items[0].Percent != 40 {
		t.Fatalf("items: %+v", b.Income.Items)
	}
	if b.Expenses.Items[1].Actual != 0 {
		t.Fatalf("gear should have nothing this year: %+v", b.Expenses.Items[1])
	}

	rec = e.do("GET", "/api/v1/budgets", e.board, nil)
	expect(t, rec, http.StatusOK, "")
	list := decode[[]budgets.Summary](t, rec)
	if len(list) != 1 || list[0].Year != 2031 || list[0].ExpensesAmount != 1500.5 {
		t.Fatalf("list: %+v", list)
	}
	if !strings.Contains(e.notifier.wait(t), "2031") {
		t.Fatal("expected a notification about the creation")
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("GET", "/api/v1/budgets", "", nil), http.StatusUnauthorized, "")
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.board, e.validBody()), http.StatusForbidden, "")
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.member, e.validBody()), http.StatusForbidden, "")
	expect(t, e.do("DELETE", "/api/v1/budgets/2031", e.member, nil), http.StatusForbidden, "")
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.admin, e.validBody()), http.StatusOK, "")
	expect(t, e.do("DELETE", "/api/v1/budgets/2031", e.money, nil), http.StatusNoContent, "")
	expect(t, e.do("GET", "/api/v1/budgets/2031", e.admin, nil), http.StatusNotFound, "not_found")
	expect(t, e.do("DELETE", "/api/v1/budgets/2031", e.admin, nil), http.StatusNotFound, "not_found")
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	group := e.account(t, "ZZ Group", "expense", nil)
	child := e.account(t, "ZZ Child", "", &group) // inherits expense, but then the group is not budgetable
	hidden := e.account(t, "ZZ Hidden", "expense", nil)
	if _, err := e.tx.Exec(context.Background(), `UPDATE accounts SET hidden = true WHERE id = $1`, hidden); err != nil {
		t.Fatal(err)
	}
	withChild := func(body map[string]any) map[string]any {
		body["expenses"] = append(body["expenses"].([]item), item{"name": "Child", "amount": 0, "account_ids": []int64{child}})
		return body
	}
	cases := []struct {
		name   string
		year   string
		mutate func(map[string]any) map[string]any
		status int
		code   string
	}{
		{"bad year", "1800", nil, 400, "invalid_year"},
		{"non numeric year", "abc", nil, 400, "invalid_year"},
		{"no name", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[0]["name"] = "  "
			return withChild(b)
		}, 400, "invalid_item"},
		{"three decimals", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[0]["amount"] = 1000.005
			return withChild(b)
		}, 400, "invalid_item"},
		{"negative", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[0]["amount"] = -1
			return withChild(b)
		}, 400, "invalid_item"},
		{"duplicate name", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["name"] = "sales"
			return withChild(b)
		}, 400, "duplicate_item"},
		{"no accounts", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["account_ids"] = []int64{}
			return withChild(b)
		}, 400, "item_without_accounts"},
		{"unknown account", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["account_ids"] = []int64{-5}
			return withChild(b)
		}, 400, "unknown_account"},
		{"group", "2031", func(b map[string]any) map[string]any {
			b["expenses"] = append(b["expenses"].([]item), item{"name": "G", "amount": 0, "account_ids": []int64{group}})
			return withChild(b)
		}, 400, "account_not_budgetable"},
		{"asset account", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["account_ids"] = []int64{e.fees, e.bank}
			return withChild(b)
		}, 400, "account_type_mismatch"},
		{"expense in income", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["account_ids"] = []int64{e.rent}
			return withChild(b)
		}, 400, "account_type_mismatch"},
		{"account twice", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[1]["account_ids"] = []int64{e.fees, e.sales}
			return withChild(b)
		}, 400, "duplicate_account"},
		{"account missing", "2031", func(b map[string]any) map[string]any {
			b["income"] = b["income"].([]item)[:1]
			b["income"].([]item)[0]["amount"] = 1500.5
			return withChild(b)
		}, 409, "accounts_missing"},
		{"unbalanced", "2031", func(b map[string]any) map[string]any {
			b["income"].([]item)[0]["amount"] = 1
			return withChild(b)
		}, 409, "unbalanced"},
		{"valid, hidden left out", "2031", withChild, 200, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := e.validBody()
			if c.mutate != nil {
				body = c.mutate(body)
			}
			expect(t, e.do("PUT", "/api/v1/budgets/"+c.year, e.admin, body), c.status, c.code)
		})
	}
	t.Run("malformed", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/api/v1/budgets/2031", strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer "+e.admin)
		rec := httptest.NewRecorder()
		e.mux.ServeHTTP(rec, req)
		expect(t, rec, http.StatusBadRequest, "invalid_body")
	})
}

func TestMissingNamesAndUnbalancedTotals(t *testing.T) {
	e := newEnv(t)
	body := e.validBody()
	body["income"] = body["income"].([]item)[:1]
	rec := e.do("PUT", "/api/v1/budgets/2031", e.admin, body)
	expect(t, rec, http.StatusConflict, "accounts_missing")
	if !strings.Contains(rec.Body.String(), "ZZ Fees") {
		t.Fatalf("message should name the account: %s", rec.Body.String())
	}
	body = e.validBody()
	body["income"].([]item)[0]["amount"] = 1
	rec = e.do("PUT", "/api/v1/budgets/2031", e.admin, body)
	expect(t, rec, http.StatusConflict, "unbalanced")
	if !strings.Contains(rec.Body.String(), "501.50") || !strings.Contains(rec.Body.String(), "1500.50") {
		t.Fatalf("message should carry both totals: %s", rec.Body.String())
	}
}

func TestReplaceUnassignedAndNotification(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.admin, e.validBody()), http.StatusOK, "")
	e.notifier.wait(t)

	// An account created afterwards shows up as unassigned, with what it moved.
	late := e.account(t, "ZZ Late", "income", nil)
	e.post(t, late, e.bank, 70, "2031-06-01")
	b := decode[budgets.Budget](t, e.do("GET", "/api/v1/budgets/2031", e.admin, nil))
	if len(b.UnassignedAccounts) != 1 || b.UnassignedAccounts[0].ID != late || b.UnassignedAccounts[0].Actual != 70 ||
		b.UnassignedAccounts[0].Kind != budgets.KindIncome {
		t.Fatalf("unassigned: %+v", b.UnassignedAccounts)
	}
	// The next save has to include it.
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.admin, e.validBody()), http.StatusConflict, "accounts_missing")

	body := e.validBody()
	body["income"].([]item)[1]["account_ids"] = []int64{e.fees, late}
	body["income"].([]item)[1]["amount"] = 600.5
	body["expenses"].([]item)[0]["amount"] = 1300.5
	body["expenses"] = append(body["expenses"].([]item)[:1], item{"name": "Other", "amount": 300, "account_ids": []int64{e.gear}})
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.admin, body), http.StatusOK, "")
	msg := e.notifier.wait(t)
	for _, want := range []string{"2031", "«Fees»", "500,50 € → 600,50 €", "ZZ Fees, ZZ Late", "añadida: «Other»", "quitada: «Gear»"} {
		if !strings.Contains(msg, want) {
			t.Errorf("notification %q lacks %q", msg, want)
		}
	}
	b = decode[budgets.Budget](t, e.do("GET", "/api/v1/budgets/2031", e.admin, nil))
	if len(b.UnassignedAccounts) != 0 || len(b.Income.Items) != 2 || len(b.Expenses.Items) != 2 {
		t.Fatalf("after replace: %+v", b)
	}
	// Saving the same thing again says nothing.
	expect(t, e.do("PUT", "/api/v1/budgets/2031", e.admin, body), http.StatusOK, "")
	expect(t, e.do("DELETE", "/api/v1/budgets/2031", e.admin, nil), http.StatusNoContent, "")
	if !strings.Contains(e.notifier.wait(t), "borrado") {
		t.Fatal("expected a notification about the deletion")
	}
}

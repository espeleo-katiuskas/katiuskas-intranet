package distributions_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/distributions"
)

// newAccount inserts an account of the given type (empty: none) and returns its id.
func (e *env) newAccount(t *testing.T, name, kind string) int64 {
	t.Helper()
	var id int64
	var k any
	if kind != "" {
		k = kind
	}
	if err := e.tx.QueryRow(context.Background(), `
		INSERT INTO accounts (name, type, position) VALUES ($1, $2::account_type, 10) RETURNING id
	`, name, k).Scan(&id); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return id
}

// nameOf is how a person's name reads, "Nombre Apellidos".
func (e *env) nameOf(t *testing.T, p person) string {
	t.Helper()
	var name string
	if err := e.tx.QueryRow(context.Background(), `SELECT first_name || ' ' || last_name FROM users WHERE id = $1`, p.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

type movement struct {
	account     string
	amount      float64
	description string
}

// movements returns the entries of every transaction, oldest first.
func (e *env) movements(t *testing.T) []movement {
	t.Helper()
	rows, err := e.tx.Query(context.Background(), `
		SELECT a.name, te.amount::float8, t.description
		FROM transaction_entries te
		JOIN transactions t ON t.id = te.transaction_id
		JOIN accounts a ON a.id = te.account_id
		ORDER BY t.id, te.amount DESC
	`)
	if err != nil {
		t.Fatalf("query movements: %v", err)
	}
	defer rows.Close()
	var out []movement
	for rows.Next() {
		var m movement
		if err := rows.Scan(&m.account, &m.amount, &m.description); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestPayingRecordsATransactionBetweenTheAssetAndTheDistributionsAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	pay := distributionPath(d.ID) + "/payments"

	mustStatus(t, e.do("POST", pay, admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	got := e.movements(t)
	if len(got) != 2 || got[0].account != "Caja" || got[0].amount != 15 || got[1].account != "Repartos" || got[1].amount != -15 {
		t.Fatalf("unexpected entries: %+v", got)
	}
	want := "Pago de " + e.nameOf(t, a) + " en «Lotería de Navidad»: 3 × Papeleta"
	if got[0].description != want {
		t.Errorf("expected description %q, got %q", want, got[0].description)
	}

	// Taking it off moves the money back, with the signs changed.
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d/payment?quantity=1", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusNoContent)
	got = e.movements(t)
	if len(got) != 4 || got[2].account != "Repartos" || got[2].amount != 5 || got[3].account != "Caja" || got[3].amount != -5 ||
		!strings.HasPrefix(got[2].description, "Devolución del pago de "+e.nameOf(t, a)) {
		t.Fatalf("unexpected entries after taking off: %+v", got)
	}
}

func TestEachPersonPaidInOneRequestGetsATransactionWithTheNumbers(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	b := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	ua := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	ub := decode[distributions.Unit](t, mustStatus(t, ask(e, d, b, 0, 1), http.StatusCreated))
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), ua.ID), admin.token, map[string]any{"identifiers": []string{"0001-0003"}}), http.StatusOK)
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), ub.ID), admin.token, map[string]any{"identifiers": []string{"0004"}}), http.StatusOK)
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{ua.ID, ub.ID}}), http.StatusNoContent)
	got := e.movements(t)
	if len(got) != 4 || got[0].amount != 15 || got[2].amount != 5 {
		t.Fatalf("expected one transaction for each person: %+v", got)
	}
	if !strings.HasSuffix(got[0].description, "3 × Papeleta (0001-0003)") {
		t.Errorf("the description should name the numbers: %q", got[0].description)
	}
}

func TestThePaymentAccountsAreChecked(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 1), http.StatusCreated))
	pay := distributionPath(d.ID) + "/payments"
	body := func(account any) map[string]any {
		return map[string]any{"unit_ids": []int64{u.ID}, "account_id": account}
	}
	expect(t, e.do("POST", pay, admin.token, body(nil)), http.StatusBadRequest, "account_required")
	expect(t, e.do("POST", pay, admin.token, body(999999)), http.StatusBadRequest, "unknown_account")
	expect(t, e.do("POST", pay, admin.token, body(e.newAccount(t, "Gastos", "expense"))), http.StatusBadRequest, "account_not_asset")
	expect(t, e.do("POST", pay, admin.token, body(e.newAccount(t, "Sin tipo", ""))), http.StatusBadRequest, "account_not_asset")

	// The distribution's own account has to be an income one.
	own := e.newAccount(t, "Ingresos propios", "income")
	notIncome := lottery()
	notIncome["account_id"] = e.cash
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, notIncome), http.StatusBadRequest, "account_not_income")
	expect(t, e.do("POST", "/api/v1/distributions", admin.token, notIncome), http.StatusBadRequest, "account_not_income")
	b := lottery()
	b["status"] = "open"
	b["account_id"] = own
	got := decode[distributions.Distribution](t, mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, b), http.StatusOK))
	if got.AccountID == nil || *got.AccountID != own || deref(got.AccountName) != "Ingresos propios" {
		t.Errorf("unexpected account: %+v", got)
	}
	// Nothing was paid by any of the failures.
	if n := len(e.movements(t)); n != 0 {
		t.Errorf("no transaction should exist: %d entries", n)
	}
	mustStatus(t, e.do("POST", pay, admin.token, body(e.cash)), http.StatusNoContent)
	if got := e.movements(t); len(got) != 2 || got[1].account != "Ingresos propios" || got[1].amount != -5 {
		t.Errorf("the distribution's own account should be used: %+v", got)
	}

	// An unknown account for the distribution itself.
	b["account_id"] = 999999
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, b), http.StatusBadRequest, "unknown_account")
}

func TestWithoutAnAccountForTheDistributionPayingIsRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	if _, err := e.tx.Exec(context.Background(), `UPDATE accounts SET distribution_default = false`); err != nil {
		t.Fatal(err)
	}
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 1), http.StatusCreated))
	expect(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusConflict, "no_distribution_account")
}

func TestPayingWhatIsFreeNeedsNoAccountAndRecordsNothing(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	body := lottery()
	body["articles"] = []map[string]any{{"name": "Pegatina", "price": 0}}
	d := newDeliveringLottery(t, e, admin, body)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 2), http.StatusCreated))
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}, "account_id": nil}), http.StatusNoContent)
	if n := len(e.movements(t)); n != 0 {
		t.Errorf("nothing should be recorded for free things: %d entries", n)
	}
}

func TestPaymentsListPointsAtTheirTransaction(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 1), http.StatusCreated))
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	var linked int
	if err := e.tx.QueryRow(context.Background(), `SELECT count(*) FROM distribution_payments WHERE transaction_id IS NOT NULL`).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("expected the entry to point at its transaction: %d, %v", linked, err)
	}
}

func (e *env) transactionsOfPayments(t *testing.T) []int64 {
	t.Helper()
	rows, err := e.tx.Query(context.Background(), `SELECT DISTINCT transaction_id FROM distribution_payments WHERE transaction_id IS NOT NULL ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (e *env) paidQuantity(t *testing.T, unitID int64) (paid int, entries int) {
	t.Helper()
	ctx := context.Background()
	if err := e.tx.QueryRow(ctx, `SELECT paid_quantity FROM distribution_units WHERE id = $1`, unitID).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if err := e.tx.QueryRow(ctx, `SELECT count(*) FROM distribution_payments`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	return paid, entries
}

func TestUndoingThePaymentsOfATransactionLeavesTheOrderAsItWas(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newDeliveringLottery(t, e, admin, lotteryWithNumbers())
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 3), http.StatusCreated))
	mustStatus(t, e.do("POST", fmt.Sprintf("%s/%d/identifiers", unitsPath(d.ID), u.ID), admin.token, map[string]any{"identifiers": []string{"0001-0003"}}), http.StatusOK)
	pay := distributionPath(d.ID) + "/payments"
	mustStatus(t, e.do("POST", pay, admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	first := e.transactionsOfPayments(t)[0]

	// A payment taken off is undone by deleting its transaction: it is paid again.
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d/payment?identifiers=0002", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusNoContent)
	reversal := e.transactionsOfPayments(t)[1]
	if err := distributions.UndoPaymentsOf(ctx, e.tx, reversal); err != nil {
		t.Fatalf("undo the reversal: %v", err)
	}
	if paid, entries := e.paidQuantity(t, u.ID); paid != 3 || entries != 1 {
		t.Fatalf("expected 3 paid and 1 entry, got %d and %d", paid, entries)
	}
	// Undoing the payment itself: nothing paid, nothing in the list.
	if err := distributions.UndoPaymentsOf(ctx, e.tx, first); err != nil {
		t.Fatalf("undo the payment: %v", err)
	}
	if paid, entries := e.paidQuantity(t, u.ID); paid != 0 || entries != 0 {
		t.Fatalf("expected nothing paid and no entries, got %d and %d", paid, entries)
	}
	var paidItems int
	if err := e.tx.QueryRow(ctx, `SELECT count(*) FROM distribution_unit_identifiers WHERE unit_id = $1 AND paid_at IS NOT NULL`, u.ID).Scan(&paidItems); err != nil || paidItems != 0 {
		t.Errorf("the numbers should be unpaid: %d, %v", paidItems, err)
	}
	// It can be paid again afterwards.
	mustStatus(t, e.do("POST", pay, admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
}

func TestPaymentsCantBeUndoneOnceTheOrderChanged(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 2), http.StatusCreated))
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusNoContent)
	tid := e.transactionsOfPayments(t)[0]
	// The same payment taken off by hand, so there is nothing left to unpay.
	mustStatus(t, e.do("DELETE", fmt.Sprintf("%s/%d/payment", unitsPath(d.ID), u.ID), admin.token, nil), http.StatusNoContent)
	if err := distributions.UndoPaymentsOf(context.Background(), e.tx, tid); err != distributions.ErrPaymentsNotUndoable {
		t.Fatalf("expected ErrPaymentsNotUndoable, got %v", err)
	}
	if paid, entries := e.paidQuantity(t, u.ID); paid != 0 || entries != 2 {
		t.Errorf("nothing should have changed: %d paid, %d entries", paid, entries)
	}
}

func TestAccountsThatGroupOthersCantBePaidInOrNamed(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 1), http.StatusCreated))
	child := func(parent int64, name, kind string) {
		t.Helper()
		if _, err := e.tx.Exec(context.Background(), `INSERT INTO accounts (name, type, parent_id, position) VALUES ($1, $2::account_type, $3, 1)`, name, kind, parent); err != nil {
			t.Fatal(err)
		}
	}
	// The asset account groups another one now.
	child(e.cash, "Cuenta del banco", "asset")
	expect(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}}), http.StatusConflict, "account_has_children")
	// And so does an income account a distribution would name.
	income := e.newAccount(t, "Ingresos agrupados", "income")
	child(income, "Ingresos hijos", "income")
	b := lottery()
	b["account_id"] = income
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, b), http.StatusConflict, "account_has_children")
}

func TestAccountsInheritTheirTypeForPaymentsAndDistributions(t *testing.T) {
	e := newEnv(t)
	admin := e.newPerson(t, "", "admin")
	a := e.newPerson(t, "2020-01-01", "member")
	d := newOpenLottery(t, e, admin)
	u := decode[distributions.Unit](t, mustStatus(t, ask(e, d, a, 0, 1), http.StatusCreated))
	sub := func(parent int64, name string) int64 {
		t.Helper()
		var id int64
		if err := e.tx.QueryRow(context.Background(), `INSERT INTO accounts (name, parent_id, position) VALUES ($1, $2, 1) RETURNING id`, name, parent).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// A typeless account under an asset is an asset to pay in, with no type of its own.
	bank := sub(e.cash, "Cuenta del banco")
	mustStatus(t, e.do("POST", distributionPath(d.ID)+"/payments", admin.token, map[string]any{"unit_ids": []int64{u.ID}, "account_id": bank}), http.StatusNoContent)
	// And one under an income account is an income account for a distribution.
	income := e.newAccount(t, "Ingresos", "income")
	b := lottery()
	b["status"] = "open"
	b["account_id"] = sub(income, "Lotería")
	mustStatus(t, e.do("PATCH", distributionPath(d.ID), admin.token, b), http.StatusOK)
	// Under an asset, it isn't.
	b["account_id"] = sub(e.cash, "Otra del banco")
	expect(t, e.do("PATCH", distributionPath(d.ID), admin.token, b), http.StatusBadRequest, "account_not_income")
}

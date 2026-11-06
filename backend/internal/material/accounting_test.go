package material_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/material"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// entriesOf reads the entries of a transaction by account.
func entriesOf(t *testing.T, tx pgx.Tx, transactionID int64) (description string, amounts map[int64]float64) {
	t.Helper()
	ctx := context.Background()
	if err := tx.QueryRow(ctx, `SELECT description FROM transactions WHERE id = $1`, transactionID).Scan(&description); err != nil {
		t.Fatalf("transaction %d: %v", transactionID, err)
	}
	rows, err := tx.Query(ctx, `SELECT account_id, amount::float8 FROM transaction_entries WHERE transaction_id = $1`, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	amounts = map[int64]float64{}
	for rows.Next() {
		var id int64
		var amount float64
		if err := rows.Scan(&id, &amount); err != nil {
			t.Fatal(err)
		}
		amounts[id] = amount
	}
	return description, amounts
}

// A sale or a rental is recorded in the accounting as it is made: the money goes into the asset
// account it was paid in and out of the income account marked for sales or for rentals, and the
// movement points at the transaction; deleting the movement deletes it.
func TestSaleAndRentalAreRecordedInTheAccounting(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser, adminUser := testutil.InsertMember(t, tx), testutil.InsertMember(t, tx)
	board := tokenFor(t, authn, boardUser.ID, "board")
	admin := tokenFor(t, authn, adminUser.ID, "admin")
	cash := testutil.MaterialAccounts(t, tx)
	var rentalAccount, saleAccount int64
	if err := tx.QueryRow(context.Background(), `SELECT id FROM accounts WHERE material_rental`).Scan(&rentalAccount); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `SELECT id FROM accounts WHERE material_sale`).Scan(&saleAccount); err != nil {
		t.Fatal(err)
	}
	buyer := testutil.InsertMember(t, tx)

	body := generalItemBody(t, tx, 10) // rental 10 per week, sale 25
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", board, body)
	expectStatus(t, rec, http.StatusCreated, "")
	item := decodeInto[material.Item](t, rec)

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", board, map[string]any{
		"item_id": item.ID, "login": buyer.Login, "type": "sale", "quantity": 2, "account_id": cash,
	})
	expectStatus(t, rec, http.StatusCreated, "")
	sale := decodeInto[material.Movement](t, rec)
	if sale.TransactionID == nil {
		t.Fatalf("the sale has no transaction: %+v", sale)
	}
	description, amounts := entriesOf(t, tx, *sale.TransactionID)
	if amounts[cash] != 50 || amounts[saleAccount] != -50 || len(amounts) != 2 {
		t.Errorf("a sale of 2 at 25: %v", amounts)
	}
	want := fmt.Sprintf("Venta de 2 × %s a ", item.DisplayName)
	if len(description) < len(want) || description[:len(want)] != want {
		t.Errorf("description %q does not start with %q", description, want)
	}

	// A rental charges one week of each unit.
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", board, map[string]any{
		"item_id": item.ID, "login": buyer.Login, "type": "rental", "quantity": 3, "account_id": cash,
	})
	expectStatus(t, rec, http.StatusCreated, "")
	rental := decodeInto[material.Movement](t, rec)
	_, amounts = entriesOf(t, tx, *rental.TransactionID)
	if amounts[cash] != 30 || amounts[rentalAccount] != -30 {
		t.Errorf("a rental of 3 at 10 a week: %v", amounts)
	}
	// Returning the rental changes nothing in the accounting.
	expectStatus(t, doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/material/movements/%d/return", rental.ID), board, nil), http.StatusOK, "")

	// The transaction of a movement cannot be deleted from the accounting: the movement goes first.
	err := accounting.NewRepository(tx).DeleteTransaction(context.Background(), *sale.TransactionID)
	if !errors.Is(err, accounting.ErrTransactionHasMaterialMovement) {
		t.Errorf("deleting the transaction of a sale: expected ErrTransactionHasMaterialMovement, got %v", err)
	}
	// Deleting the movement deletes its transaction.
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/movements/%d", sale.ID), admin, nil), http.StatusNoContent, "")
	var left int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM transactions WHERE id = $1`, *sale.TransactionID).Scan(&left); err != nil || left != 0 {
		t.Errorf("the transaction of the deleted sale is still there (%d, %v)", left, err)
	}
}

// What costs nothing records nothing and needs no account; money needs a valid asset account, and
// an income account marked for it.
func TestMovementAccountsAreChecked(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	board := tokenFor(t, authn, testutil.InsertMember(t, tx).ID, "board")
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", board, generalItemBody(t, tx, 10))
	expectStatus(t, rec, http.StatusCreated, "")
	item := decodeInto[material.Item](t, rec)
	sale := func(account any) *httptest.ResponseRecorder {
		body := map[string]any{"item_id": item.ID, "type": "sale", "quantity": 1}
		if account != nil {
			body["account_id"] = account
		}
		return doRequest(mux, http.MethodPost, "/api/v1/material/movements", board, body)
	}

	// No account marked for sales yet: there is nowhere to record the income.
	var asset int64
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO accounts (name, code, type, position) VALUES ('Caja', 'TEST-CASH', 'asset', 1) RETURNING id`).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, sale(asset), http.StatusConflict, "no_material_account")

	cash := testutil.MaterialAccounts(t, tx)
	expectStatus(t, sale(nil), http.StatusBadRequest, "account_required")
	expectStatus(t, sale(987654), http.StatusBadRequest, "unknown_account")
	var income int64
	if err := tx.QueryRow(context.Background(), `SELECT id FROM accounts WHERE material_sale`).Scan(&income); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, sale(income), http.StatusBadRequest, "account_not_asset")
	// An account that groups others cannot be paid into.
	if _, err := tx.Exec(context.Background(), `INSERT INTO accounts (name, code, parent_id, position) VALUES ('Hija', 'TEST-CHILD', $1, 1)`, cash); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, sale(cash), http.StatusConflict, "account_has_children")
	// None of the refused sales left anything behind.
	var movements int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM material_movements WHERE item_id = $1 AND type = 'sale'`, item.ID).Scan(&movements); err != nil || movements != 0 {
		t.Errorf("refused sales left %d movements (%v)", movements, err)
	}

	// An item that costs nothing needs no account and records no transaction.
	free := generalItemBody(t, tx, 5)
	free["description"] = "Gratis"
	free["sale_price"] = 0.0
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/items", board, free)
	expectStatus(t, rec, http.StatusCreated, "")
	freeItem := decodeInto[material.Item](t, rec)
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", board, map[string]any{"item_id": freeItem.ID, "type": "sale", "quantity": 1})
	expectStatus(t, rec, http.StatusCreated, "")
	if m := decodeInto[material.Movement](t, rec); m.TransactionID != nil {
		t.Errorf("a sale that costs nothing recorded a transaction: %+v", m)
	}
}

package inventory_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/inventory"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	service := inventory.NewService(inventory.NewRepository(tx))
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	inventory.NewHandler(service, authenticator, usersService).RegisterRoutes(mux)
	return mux, resolver, tx
}

func tokenFor(t *testing.T, resolver testutil.FakeResolver, id int64, roleNames ...string) string {
	t.Helper()
	return testutil.TokenFor(resolver, id, roleNames...)
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

// gearItemBody is a valid POST /items body for a gear item — the shape
// most of these handler tests just need to exist, not exercise the
// per-type rope/bag/gear rules themselves.
// rental_price_per_week/sale_price are set so tests can create a
// rental or sale transaction against this item without needing to give
// a price themselves — it's always taken from the item now.
func gearItemBody(t *testing.T, tx pgx.Tx, totalQuantity int) map[string]any {
	t.Helper()
	return map[string]any{
		"item_type":             "gear",
		"description":           "Casco",
		"category_id":           testutil.MaterialCategoryID(t, tx, "Mosquetones"),
		"total_quantity":        totalQuantity,
		"rental_price_per_week": 10.0,
		"sale_price":            25.0,
	}
}

func TestHandlerCreateItemRequiresAdminOrBoard(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	materialToken := tokenFor(t, authn, 2, "material")
	boardToken := tokenFor(t, authn, 3, "board")

	body := gearItemBody(t, tx, 1)
	if rec := doRequest(mux, http.MethodPost, "/api/v1/items", memberToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from creating an item, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/items", materialToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected the material role alone to be forbidden from creating an item, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, body); rec.Code != http.StatusCreated {
		t.Errorf("expected board to create an item, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The API-wide baseline (see auth.Protect) requires admin/board/member
// on every route, on top of whatever else that route requires — so
// "material" alone, without also being a current member (or admin/
// board), isn't enough for the material endpoints either.
func TestHandlerTransactionsAllowMaterialPlusMemberButNotMaterialAlone(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	materialOnlyUser := testutil.InsertUser(t, tx, "")
	materialMemberUser := testutil.InsertUser(t, tx, "")
	memberUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	materialOnlyToken := tokenFor(t, authn, materialOnlyUser.ID, "material")
	materialMemberToken := tokenFor(t, authn, materialMemberUser.ID, "material", "member")
	memberToken := tokenFor(t, authn, memberUser.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	activityID := testutil.InsertActivity(t, tx)
	user := testutil.InsertUser(t, tx, "")

	body := map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": activityID,
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", memberToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from creating a transaction, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/transactions", materialOnlyToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected \"material\" alone (no admin/board/member) to be forbidden, got %d", rec.Code)
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/transactions", materialMemberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected material+member to create a transaction, got %d: %s", rec.Code, rec.Body.String())
	}

	var txn inventory.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &txn); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	returnPath := fmt.Sprintf("/api/v1/transactions/%d/return", txn.ID)
	if rec := doRequest(mux, http.MethodPatch, returnPath, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from returning a transaction, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, returnPath, materialMemberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected material+member to register a return, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListTransactionsByUserSelfOrPrivilegedOnly(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	targetToken := tokenFor(t, authn, target.ID, "member")
	otherToken := tokenFor(t, authn, other.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/transactions", boardToken, map[string]any{
		"item_id": item.ID, "login": target.Login, "type": "sale", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transaction: %d: %s", rec.Code, rec.Body.String())
	}

	path := fmt.Sprintf("/api/v1/users/%s/transactions", target.Login)
	if rec := doRequest(mux, http.MethodGet, path, otherToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected another plain member to be forbidden, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, path, targetToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected the person themselves to view their own history (200), got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(mux, http.MethodGet, path, boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected board to view anyone's history (200), got %d: %s", rec.Code, rec.Body.String())
	}
	var list []inventory.Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 transaction, got %d", len(list))
	}
}

func TestHandlerCreateTransactionRequiresActivityForLoan(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/transactions", boardToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a loan without an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "activity_required" {
		t.Errorf("expected error code activity_required, got %q", code)
	}
}

func TestHandlerCreateItemRejectsInvalidItemType(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "not-a-type", "total_quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid item_type, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_item_type" {
		t.Errorf("expected error code invalid_item_type, got %q", code)
	}
}

func TestHandlerCreateItemRejectsUnknownCategory(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "gear", "description": "Casco", "category_id": 999999, "total_quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown category_id, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_category" {
		t.Errorf("expected error code unknown_category, got %q", code)
	}
}

func TestHandlerCreateTransactionRejectsLoanOfNonLoanableItem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "gear", "description": "Casco",
		"category_id":    testutil.MaterialCategoryID(t, tx, "Mosquetones"),
		"total_quantity": 1, "loanable": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Loanable {
		t.Fatalf("expected the created item to be non-loanable, got %+v", item)
	}
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	body := map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": activityID,
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/transactions", boardToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a loan of a non-loanable item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_loanable" {
		t.Errorf("expected error code not_loanable, got %q", code)
	}
}

func TestHandlerListMaterialCategoriesRequiresAdminOrBoard(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardToken := tokenFor(t, authn, 2, "board")

	if rec := doRequest(mux, http.MethodGet, "/api/v1/material-categories", memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from listing material categories, got %d", rec.Code)
	}
	rec := doRequest(mux, http.MethodGet, "/api/v1/material-categories", boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected board to list material categories, got %d: %s", rec.Code, rec.Body.String())
	}
	var categories []inventory.MaterialCategory
	if err := json.Unmarshal(rec.Body.Bytes(), &categories); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(categories) == 0 {
		t.Error("expected the seeded material categories to be listed")
	}
}

package inventory_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/inventory"
	"katiuskas-intranet/backend/internal/testutil"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	service := inventory.NewService(inventory.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	inventory.NewHandler(service, authenticator).RegisterRoutes(mux)
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

func TestHandlerCreateItemRequiresAdminOrBoard(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	materialToken := tokenFor(t, authn, 2, "material")
	boardToken := tokenFor(t, authn, 3, "board")

	body := map[string]any{"name": "Casco", "total_quantity": 1}
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{"name": "Casco", "total_quantity": 5})
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
		"item_id": item.ID, "user_id": user.ID, "type": "loan", "quantity": 1, "activity_id": activityID,
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

func TestHandlerCreateTransactionRequiresActivityForLoan(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{"name": "Casco", "total_quantity": 5})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")

	body := map[string]any{"item_id": item.ID, "user_id": user.ID, "type": "loan", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/transactions", boardToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a loan without an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "activity_required" {
		t.Errorf("expected error code activity_required, got %q", code)
	}
}

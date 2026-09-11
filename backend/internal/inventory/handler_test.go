package inventory_test

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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/inventory"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	mux, resolver, tx, _, _ := newTestServerWithServices(t, nil)
	return mux, resolver, tx
}

// newTestServerWithServices is newTestServer, but also returns the
// inventory.Service and users.Service backing it (for tests that need
// to create an item/movement directly, or resolve a login), and wires
// notifier — pass nil (like newTestServer does) when a test doesn't
// care about mutation notifications.
func newTestServerWithServices(t *testing.T, notifier httpapi.MutationNotifier) (*http.ServeMux, testutil.FakeResolver, pgx.Tx, *inventory.Service, *users.Service) {
	tx := testutil.NewTx(t)
	service := inventory.NewService(inventory.NewRepository(tx))
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	inventory.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
	return mux, resolver, tx, service, usersService
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

// waitForNthMessage polls until at least n+1 messages have arrived and
// returns the nth (0-indexed) — for a test flow that triggers more than
// one notification (creating an item, then a movement against it), so
// each assertion can target the specific message it cares about instead
// of guessing at delivery order between two independent goroutines.
func (f *fakeNotifier) waitForNthMessage(t *testing.T, n int) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > n {
			msg := f.sent[n]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for mutation notification #%d", n)
	return ""
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
// rental or sale movement against this item without needing to give
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
func TestHandlerMovementsAllowMaterialPlusMemberButNotMaterialAlone(t *testing.T) {
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
	if rec := doRequest(mux, http.MethodPost, "/api/v1/movements", memberToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from creating a movement, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/movements", materialOnlyToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected \"material\" alone (no admin/board/member) to be forbidden, got %d", rec.Code)
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", materialMemberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected material+member to create a movement, got %d: %s", rec.Code, rec.Body.String())
	}

	// Loan never reaches 'active' (only rental does), so the return
	// permission check below needs a rental to actually close.
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", materialMemberToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rental: %d: %s", rec.Code, rec.Body.String())
	}
	var rental inventory.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &rental); err != nil {
		t.Fatalf("decode rental: %v", err)
	}
	returnPath := fmt.Sprintf("/api/v1/movements/%d/return", rental.ID)
	if rec := doRequest(mux, http.MethodPatch, returnPath, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from returning a movement, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, returnPath, materialMemberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected material+member to register a return, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListMovementsByUserSelfOrPrivilegedOnly(t *testing.T) {
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
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": target.Login, "type": "sale", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create movement: %d: %s", rec.Code, rec.Body.String())
	}

	path := fmt.Sprintf("/api/v1/users/%s/movements", target.Login)
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
	var list []inventory.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 movement, got %d", len(list))
	}
}

func TestHandlerCreateMovementRequiresActivityForLoan(t *testing.T) {
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
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, body)
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

func TestHandlerCreateMovementRejectsLoanOfNonLoanableItem(t *testing.T) {
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
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a loan of a non-loanable item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_loanable" {
		t.Errorf("expected error code not_loanable, got %q", code)
	}
}

// TestHandlerCreateMovementRejectsRentalOfNonRentableItem and
// TestHandlerCreateMovementRejectsSaleOfNonSellableItem mirror
// TestHandlerCreateMovementRejectsLoanOfNonLoanableItem above, for the
// two movement types whose own "can this happen" flag is instead
// derived from a price being on file (see Item.Rentable/Item.Sellable).
func TestHandlerCreateMovementRejectsRentalOfNonRentableItem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "gear", "description": "Casco",
		"category_id":    testutil.MaterialCategoryID(t, tx, "Mosquetones"),
		"total_quantity": 1, "loanable": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Rentable {
		t.Fatalf("expected the created item not to be rentable with no rental price given, got %+v", item)
	}
	user := testutil.InsertUser(t, tx, "")

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "rental", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a rental of a non-rentable item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_rentable" {
		t.Errorf("expected error code not_rentable, got %q", code)
	}
}

func TestHandlerCreateMovementRejectsSaleOfNonSellableItem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "gear", "description": "Casco",
		"category_id":    testutil.MaterialCategoryID(t, tx, "Mosquetones"),
		"total_quantity": 1, "loanable": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Sellable {
		t.Fatalf("expected the created item not to be sellable with no sale price given, got %+v", item)
	}
	user := testutil.InsertUser(t, tx, "")

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "sale", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a sale of a non-sellable item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_sellable" {
		t.Errorf("expected error code not_sellable, got %q", code)
	}
}

// TestHandlerCreateMovementNotFoundForNonexistentItem covers the fix
// for a previously wrong status: a movement against an item id that
// doesn't exist at all used to come back 409 insufficient_stock — a
// confusing answer for an item that isn't there to have any quantity
// at all — instead of a plain 404.
func TestHandlerCreateMovementNotFoundForNonexistentItem(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	body := map[string]any{"item_id": 999999, "type": "acquisition", "quantity": 1}
	rec := doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a movement against a nonexistent item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_found" {
		t.Errorf("expected error code not_found, got %q", code)
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

func TestHandlerCreateMovementRejectsInvalidType(t *testing.T) {
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

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "not-a-type", "quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid movement type, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_type" {
		t.Errorf("expected error code invalid_type, got %q", code)
	}
}

func TestHandlerCreateMovementRequiresLoginForRental(t *testing.T) {
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

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a rental without a login, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "login_required" {
		t.Errorf("expected error code login_required, got %q", code)
	}
}

// A loan's recipient is optional now — the activity it's tied to is
// the mandatory link, not necessarily one specific person.
func TestHandlerCreateMovementAllowsLoanWithoutLogin(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "loan", "quantity": 1, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for a loan without a login, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A sale's recipient is optional now — not every sale is tied to a
// socio.
func TestHandlerCreateMovementAllowsSaleWithoutLogin(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "sale", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for a sale without a login, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Acquisition, loss, abandonment and retirement never have a
// recipient — giving one is rejected outright, not just ignored.
func TestHandlerCreateMovementRejectsLoginForAcquisition(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")
	user := testutil.InsertUser(t, tx, "")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "acquisition", "quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an acquisition with a login, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "recipient_not_allowed" {
		t.Errorf("expected error code recipient_not_allowed, got %q", code)
	}
}

// Acquisition, loss, abandonment and retirement have no recipient — a
// board member can log one without naming any socio.
func TestHandlerCreateMovementAllowsAcquisitionWithoutLogin(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	notes := "restock tras inventario"
	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "acquisition", "quantity": 3, "notes": notes,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for an acquisition without a login, got %d: %s", rec.Code, rec.Body.String())
	}
	var m inventory.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode movement: %v", err)
	}
	if m.Notes == nil || *m.Notes != notes {
		t.Errorf("expected notes to round-trip, got %+v", m.Notes)
	}
}

func TestHandlerCreateMovementRejectsAcquisitionOnRope(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, map[string]any{
		"item_type": "rope", "brand": "Beal", "model": "Antipodes", "code": "C-500",
		"rope_type": "caving", "diameter_mm": 9.5, "manufacture_year": 2024, "length_m": 60,
		"total_quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rope: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "acquisition", "quantity": 1,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for an acquisition on a rope, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "single_unit_item" {
		t.Errorf("expected error code single_unit_item, got %q", code)
	}
}

// A loan comes back via a separate 'return' movement (POST /movements,
// not PATCH .../return, which only ever applies to a rental). 30
// mosquetones lent for a trip can come back as 10 one day and 20 the
// next, each its own POST against the same item and activity.
func TestHandlerReturnTypeSupportsPartialQuantities(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 30))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 30, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create loan: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 10, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first partial return: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/items", boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", rec.Code, rec.Body.String())
	}
	var items []inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if items[0].OutstandingLoanQuantity != 20 {
		t.Fatalf("expected outstanding_loan_quantity 20 after returning 10 of 30, got %+v", items[0])
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 20, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second partial return: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/items", boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if items[0].OutstandingLoanQuantity != 0 || items[0].AvailableQuantity != 30 {
		t.Fatalf("expected everything back, got %+v", items[0])
	}
}

func TestHandlerReturnTypeRejectsOverReturn(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 5, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create loan: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 6, "activity_id": activityID,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 returning more than was loaned, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "return_exceeds_loaned" {
		t.Errorf("expected error code return_exceeds_loaned, got %q", code)
	}
}

func TestHandlerReturnTypeRequiresActivity(t *testing.T) {
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

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a return without an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "activity_required" {
		t.Errorf("expected error code activity_required, got %q", code)
	}
}

// ?activity_id= backs the activity page's own material listing, every
// loan and return tied to that activity so it can compute what's still
// outstanding item by item.
func TestHandlerListMovementsByActivity(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)
	otherActivityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	if rec := doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 3, "activity_id": activityID,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create loan for activityID: %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": otherActivityID,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create loan for otherActivityID: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/movements?activity_id=%d", activityID), boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list by activity: %d: %s", rec.Code, rec.Body.String())
	}
	var list []inventory.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Quantity != 3 {
		t.Fatalf("expected only this activity's own loan, got %+v", list)
	}
}

func TestHandlerCreateItemNotifiesTelegram(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, authn, tx, _, _ := newTestServerWithServices(t, notifier)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, boardUser.Login) {
		t.Errorf("expected the notification to name the actor %q, got %q", boardUser.Login, msg)
	}
	if !strings.Contains(msg, "Casco") {
		t.Errorf("expected the notification to name the item, got %q", msg)
	}
}

// TestHandlerCreateMovementNotifiesTelegramWithBorrower covers a rental
// (which always has a recipient), since it exercises both the item
// label and the borrower login in the same message.
func TestHandlerCreateMovementNotifiesTelegramWithBorrower(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, authn, tx, _, _ := newTestServerWithServices(t, notifier)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	borrower := testutil.InsertUser(t, tx, "")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	notifier.waitForMessage(t) // the createItem notification, checked in its own test

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": borrower.Login, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rental: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForNthMessage(t, 1)
	if !strings.Contains(msg, boardUser.Login) {
		t.Errorf("expected the notification to name the actor %q, got %q", boardUser.Login, msg)
	}
	if !strings.Contains(msg, borrower.Login) {
		t.Errorf("expected the notification to name the borrower %q, got %q", borrower.Login, msg)
	}
	if !strings.Contains(msg, "alquiler") || !strings.Contains(msg, "Casco") {
		t.Errorf("expected the notification to name the movement kind and item, got %q", msg)
	}
}

func TestHandlerReturnMovementNotifiesTelegram(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, authn, tx, _, _ := newTestServerWithServices(t, notifier)
	boardUser := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	borrower := testutil.InsertUser(t, tx, "")

	rec := doRequest(mux, http.MethodPost, "/api/v1/items", boardToken, gearItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item inventory.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": borrower.Login, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rental: %d: %s", rec.Code, rec.Body.String())
	}
	var rental inventory.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &rental); err != nil {
		t.Fatalf("decode rental: %v", err)
	}
	// #0 and #1 are the createItem/createMovement notifications, each
	// covered by their own test.
	notifier.waitForNthMessage(t, 1)

	returnPath := fmt.Sprintf("/api/v1/movements/%d/return", rental.ID)
	if rec := doRequest(mux, http.MethodPatch, returnPath, boardToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("return rental: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForNthMessage(t, 2)
	if !strings.Contains(msg, boardUser.Login) {
		t.Errorf("expected the notification to name the actor %q, got %q", boardUser.Login, msg)
	}
	if !strings.Contains(msg, borrower.Login) {
		t.Errorf("expected the notification to name the borrower %q, got %q", borrower.Login, msg)
	}
	if !strings.Contains(msg, "devolución") || !strings.Contains(msg, "Casco") {
		t.Errorf("expected the notification to name the action and item, got %q", msg)
	}
}

package material_test

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
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/material"
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
// material.Service and users.Service backing it (for tests that need
// to create an item/movement directly, or resolve a login), and wires
// notifier — pass nil (like newTestServer does) when a test doesn't
// care about mutation notifications.
func newTestServerWithServices(t *testing.T, notifier httpapi.MutationNotifier) (*http.ServeMux, testutil.FakeResolver, pgx.Tx, *material.Service, *users.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	service := material.NewService(material.NewRepository(tx), rolesService)
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	material.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
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

// generalItemBody is a valid POST /items body for a general item — the shape
// most of these handler tests just need to exist, not exercise the
// per-type rope/bag/general rules themselves.
// rental_price_per_week/sale_price are set so tests can create a
// rental or sale movement against this item without needing to give
// a price themselves — it's always taken from the item now.
func generalItemBody(t *testing.T, tx pgx.Tx, totalQuantity int) map[string]any {
	t.Helper()
	return map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Mosquetones"), "description": "Casco",
		"total_quantity":        totalQuantity,
		"rental_price_per_week": 10.0,
		"sale_price":            25.0,
	}
}

// Adding equipment to the catalog (and the types its form needs) is for admin, board and the
// material role — which, like everything on the API, also needs one of admin/board/member.
func TestHandlerCreateItemRequiresAdminBoardOrMaterial(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	materialAloneToken := tokenFor(t, authn, 2, "material")
	boardToken := tokenFor(t, authn, 3, "board")
	materialMemberToken := tokenFor(t, authn, 4, "material", "member")

	body := generalItemBody(t, tx, 1)
	for name, token := range map[string]string{"a member": memberToken, "the material role alone": materialAloneToken} {
		if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", token, body); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden from creating an item, got %d", name, rec.Code)
		}
	}
	for name, token := range map[string]string{"board": boardToken, "material (and member)": materialMemberToken} {
		if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", token, body); rec.Code != http.StatusCreated {
			t.Errorf("expected %s to create an item, got %d: %s", name, rec.Code, rec.Body.String())
		}
		if rec := doRequest(mux, http.MethodGet, "/api/v1/material/types", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list the types, got %d", name, rec.Code)
		}
	}
}

// The API-wide baseline (see auth.Protect) requires admin/board/member
// on every route, on top of whatever else that route requires — so
// "material" alone, without also being a current member (or admin/
// board), isn't enough for the material endpoints either.
func TestHandlerMovementsAllowMaterialPlusMemberButNotMaterialAlone(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertMember(t, tx)
	materialOnlyUser := testutil.InsertMember(t, tx)
	materialMemberUser := testutil.InsertMember(t, tx)
	memberUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	materialOnlyToken := tokenFor(t, authn, materialOnlyUser.ID, "material")
	materialMemberToken := tokenFor(t, authn, materialMemberUser.ID, "material", "member")
	memberToken := tokenFor(t, authn, memberUser.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	activityID := testutil.InsertActivity(t, tx)
	user := testutil.InsertMember(t, tx)

	body := map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": activityID,
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", memberToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from creating a movement, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", materialOnlyToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected \"material\" alone (no admin/board/member) to be forbidden, got %d", rec.Code)
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", materialMemberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected material+member to create a movement, got %d: %s", rec.Code, rec.Body.String())
	}

	// Loan never reaches 'active' (only rental does), so the return
	// permission check below needs a rental to actually close.
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", materialMemberToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rental: %d: %s", rec.Code, rec.Body.String())
	}
	var rental material.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &rental); err != nil {
		t.Fatalf("decode rental: %v", err)
	}
	returnPath := fmt.Sprintf("/api/v1/material/movements/%d/return", rental.ID)
	if rec := doRequest(mux, http.MethodPatch, returnPath, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from returning a movement, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, returnPath, materialMemberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected material+member to register a return, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListMovementsByUserSelfOrPrivilegedOnly(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertMember(t, tx)
	target := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	targetToken := tokenFor(t, authn, target.ID, "member")
	otherToken := tokenFor(t, authn, other.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": target.Login, "type": "sale", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create movement: %d: %s", rec.Code, rec.Body.String())
	}

	path := fmt.Sprintf("/api/v1/users/%s/material/movements", target.Login)
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
	var list []material.Movement
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	user := testutil.InsertMember(t, tx)

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a loan without an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "activity_required" {
		t.Errorf("expected error code activity_required, got %q", code)
	}
}

func TestHandlerCreateItemRejectsUnknownType(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": 999999, "description": "Casco", "total_quantity": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown type_id, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_type" {
		t.Errorf("expected error code unknown_type, got %q", code)
	}
}

// TestHandlerCreateItemRejectsMismatchedFieldsForType covers the fix
// for a previously unmapped 500: a rope with no rope-only fields set
// used to hit the database's own CHECK constraint directly, instead of
// a clear 400.
func TestHandlerCreateItemRejectsMismatchedFieldsForType(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Cuerda"), "brand": "Beal", "model": "Antipodes", "code": "C-101", "total_quantity": 1,
		// Deliberately missing rope_type/diameter_mm/manufacture_year/length_m.
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a rope missing its own required fields, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_item_fields" {
		t.Errorf("expected error code invalid_item_fields, got %q", code)
	}
}

func TestHandlerCreateMovementRejectsLoanOfNonLoanableItem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Mosquetones"), "description": "Casco",
		"total_quantity": 1, "loanable": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Loanable {
		t.Fatalf("expected the created item to be non-loanable, got %+v", item)
	}
	user := testutil.InsertMember(t, tx)
	activityID := testutil.InsertActivity(t, tx)

	body := map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": activityID,
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Mosquetones"), "description": "Casco",
		"total_quantity": 1, "loanable": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Rentable {
		t.Fatalf("expected the created item not to be rentable with no rental price given, got %+v", item)
	}
	user := testutil.InsertMember(t, tx)

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "rental", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Mosquetones"), "description": "Casco",
		"total_quantity": 1, "loanable": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if item.Sellable {
		t.Fatalf("expected the created item not to be sellable with no sale price given, got %+v", item)
	}
	user := testutil.InsertMember(t, tx)

	body := map[string]any{"item_id": item.ID, "login": user.Login, "type": "sale", "quantity": 1}
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
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
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a movement against a nonexistent item, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_found" {
		t.Errorf("expected error code not_found, got %q", code)
	}
}

// Like the items, the types can be read by anybody with a baseline role (admin, board or member).
func TestHandlerListMaterialTypesIsOpenToEveryBaselineRole(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	materialAloneToken := tokenFor(t, authn, 3, "material")

	if rec := doRequest(mux, http.MethodGet, "/api/v1/material/types", materialAloneToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected the material role alone to be forbidden, as for the items, got %d", rec.Code)
	}
	rec := doRequest(mux, http.MethodGet, "/api/v1/material/types", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a member to list material types, got %d: %s", rec.Code, rec.Body.String())
	}
	var types []material.MaterialType
	if err := json.Unmarshal(rec.Body.Bytes(), &types); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(types) == 0 {
		t.Error("expected the seeded material types to be listed")
	}
}

func TestHandlerCreateMovementRejectsInvalidType(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	user := testutil.InsertMember(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	notes := "restock tras inventario"
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "acquisition", "quantity": 3, "notes": notes,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for an acquisition without a login, got %d: %s", rec.Code, rec.Body.String())
	}
	var m material.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode movement: %v", err)
	}
	if m.Notes == nil || *m.Notes != notes {
		t.Errorf("expected notes to round-trip, got %+v", m.Notes)
	}
}

func TestHandlerCreateMovementRejectsAcquisitionOnRope(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Cuerda"), "brand": "Beal", "model": "Antipodes", "code": "C-500",
		"rope_type": "caving", "diameter_mm": 9.5, "manufacture_year": 2024, "length_m": 60,
		"total_quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rope: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertMember(t, tx)
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 30))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 30, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create loan: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 10, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first partial return: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/material/items", boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list items: %d: %s", rec.Code, rec.Body.String())
	}
	var items []material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if items[0].OutstandingLoanQuantity != 20 {
		t.Fatalf("expected outstanding_loan_quantity 20 after returning 10 of 30, got %+v", items[0])
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "type": "return", "quantity": 20, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second partial return: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/material/items", boardToken, nil)
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertMember(t, tx)
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 5, "activity_id": activityID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create loan: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	user := testutil.InsertMember(t, tx)
	activityID := testutil.InsertActivity(t, tx)
	otherActivityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 3, "activity_id": activityID,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create loan for activityID: %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": user.Login, "type": "loan", "quantity": 1, "activity_id": otherActivityID,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create loan for otherActivityID: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/material/movements?activity_id=%d", activityID), boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list by activity: %d: %s", rec.Code, rec.Body.String())
	}
	var list []material.Movement
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	borrower := testutil.InsertMember(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	notifier.waitForMessage(t) // the createItem notification, checked in its own test

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
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
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	borrower := testutil.InsertMember(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 5))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": item.ID, "login": borrower.Login, "type": "rental", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rental: %d: %s", rec.Code, rec.Body.String())
	}
	var rental material.Movement
	if err := json.Unmarshal(rec.Body.Bytes(), &rental); err != nil {
		t.Fatalf("decode rental: %v", err)
	}
	// #0 and #1 are the createItem/createMovement notifications, each
	// covered by their own test.
	notifier.waitForNthMessage(t, 1)

	returnPath := fmt.Sprintf("/api/v1/material/movements/%d/return", rental.ID)
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

// Whoever can add equipment can edit it: admin, board and material (with member, like everything).
// The type and the quantities are not editable; the rest is replaced by the body's.
func TestHandlerUpdateItem(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, authn, tx, _, _ := newTestServerWithServices(t, notifier)
	boardToken := tokenFor(t, authn, 3, "board")
	materialMemberToken := tokenFor(t, authn, 4, "material", "member")
	materialAloneToken := tokenFor(t, authn, 5, "material")
	memberToken := tokenFor(t, authn, 6, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 4))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/material/items/%d", item.ID)
	edited := map[string]any{
		"description":           "Casco con barbuquejo",
		"rental_price_per_week": 12.5,
		"loanable":              false,
		// A general item can move to another general type; its quantities are not editable (ignored).
		"type_id": testutil.MaterialTypeID(t, tx, "Placas"), "total_quantity": 99,
	}

	for name, token := range map[string]string{"a member": memberToken, "the material role alone": materialAloneToken} {
		if rec := doRequest(mux, http.MethodPatch, path, token, edited); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden from editing, got %d", name, rec.Code)
		}
	}
	rec = doRequest(mux, http.MethodPatch, path, materialMemberToken, edited)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d: %s", rec.Code, rec.Body.String())
	}
	var got material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != material.ItemKindGeneral || got.TypeName != "Placas" || got.TotalQuantity != 4 || got.Description == nil || *got.Description != "Casco con barbuquejo" ||
		got.Loanable || got.SalePrice != nil ||
		got.RentalPricePerWeek == nil || *got.RentalPricePerWeek != 12.5 {
		t.Fatalf("edited item: %+v", got)
	}
	msg := notifier.waitForNthMessage(t, 1) // the second, after the creation's
	for _, want := range []string{"ha editado el material", "Description:", "TypeName:", "SalePrice:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("notice %q lacks %q", msg, want)
		}
	}

	// The same constraints as when it was created.
	bad := map[string]any{"description": nil}
	rec = doRequest(mux, http.MethodPatch, path, boardToken, bad)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_item_fields" {
		t.Errorf("a general item without a description: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPatch, "/api/v1/material/items/987654", boardToken, edited); rec.Code != http.StatusNotFound {
		t.Errorf("an item that does not exist: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, "/api/v1/material/items/abc", boardToken, edited); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", rec.Code)
	}
}

// The type of general material can change, but only to another general type: a rope or a bag stays
// Cuerda or Saca, and nothing becomes one of them.
func TestHandlerItemTypeCanOnlyChangeWithinGeneral(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")
	create := func(body map[string]any) material.Item {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var item material.Item
		if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	patch := func(item material.Item, body map[string]any) *httptest.ResponseRecorder {
		return doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/items/%d", item.ID), boardToken, body)
	}
	cuerda, saca, placas := testutil.MaterialTypeID(t, tx, "Cuerda"), testutil.MaterialTypeID(t, tx, "Saca"), testutil.MaterialTypeID(t, tx, "Placas")

	general := create(generalItemBody(t, tx, 1))
	for name, typeID := range map[string]int64{"Cuerda": cuerda, "Saca": saca} {
		rec := patch(general, map[string]any{"description": "Casco", "type_id": typeID})
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "kind_change" {
			t.Errorf("a general item to %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := patch(general, map[string]any{"description": "Casco", "type_id": 999999}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "unknown_type" {
		t.Errorf("an unknown type: %d %s", rec.Code, rec.Body.String())
	}
	// Leaving it out keeps the type.
	rec := patch(general, map[string]any{"description": "Casco"})
	var kept material.Item
	_ = json.Unmarshal(rec.Body.Bytes(), &kept)
	if rec.Code != http.StatusOK || kept.TypeName != "Mosquetones" {
		t.Errorf("without type_id: %d %s", rec.Code, rec.Body.String())
	}
	rec = patch(general, map[string]any{"description": "Casco", "type_id": placas})
	var moved material.Item
	_ = json.Unmarshal(rec.Body.Bytes(), &moved)
	if rec.Code != http.StatusOK || moved.TypeName != "Placas" || moved.Kind != material.ItemKindGeneral {
		t.Errorf("a general item to another general type: %d %s", rec.Code, rec.Body.String())
	}

	rope := create(map[string]any{
		"type_id": cuerda, "brand": "Beal", "model": "Karma", "code": "C-T1", "rope_type": "caving",
		"diameter_mm": 9.0, "manufacture_year": 2020, "length_m": 50,
	})
	ropeBody := map[string]any{"brand": "Beal", "model": "Karma", "code": "C-T1", "rope_type": "caving",
		"diameter_mm": 9.0, "manufacture_year": 2020, "length_m": 50}
	for name, typeID := range map[string]int64{"a general type": placas, "Saca": saca} {
		ropeBody["type_id"] = typeID
		if rec := patch(rope, ropeBody); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "kind_change" {
			t.Errorf("a rope to %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	ropeBody["type_id"] = cuerda
	if rec := patch(rope, ropeBody); rec.Code != http.StatusOK {
		t.Errorf("a rope with its own type: %d %s", rec.Code, rec.Body.String())
	}
}

// A code is unique: giving an item the one another has says so, when editing and when creating.
func TestHandlerItemCodeAlreadyTaken(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 3, "board")
	rope := func(code string) map[string]any {
		return map[string]any{
			"type_id": testutil.MaterialTypeID(t, tx, "Cuerda"), "brand": "Beal", "model": "Karma", "code": code, "rope_type": "caving",
			"diameter_mm": 9.0, "manufacture_year": 2020, "length_m": 50,
		}
	}
	first := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, rope("C-1"))
	second := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, rope("C-2"))
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("create: %d %d", first.Code, second.Code)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, rope("C-1")); rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate_item_code" {
		t.Errorf("creating with a code in use: %d %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	_ = json.Unmarshal(second.Body.Bytes(), &item)
	edit := rope("C-1")
	if rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/items/%d", item.ID), boardToken, edit); rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate_item_code" {
		t.Errorf("editing to a code in use: %d %s", rec.Code, rec.Body.String())
	}
	// Keeping its own code is fine.
	own := rope("C-2")
	if rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/items/%d", item.ID), boardToken, own); rec.Code != http.StatusOK {
		t.Errorf("editing with its own code: %d %s", rec.Code, rec.Body.String())
	}
}

// A loan or a rental is only for a member; a sale, for a member or a former member; nobody else.
func TestHandlerMovementRecipientsMustBeMembers(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertMember(t, tx)
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	member := testutil.InsertMember(t, tx)
	never := testutil.InsertUser(t, tx, "")
	former := testutil.InsertUser(t, tx, "")
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO memberships (user_id, start_date, end_date) VALUES ($1, CURRENT_DATE - 400, CURRENT_DATE - 30)`, former.ID); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 20))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d: %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	activityID := testutil.InsertActivity(t, tx)

	move := func(kind string, who testutil.TestUser) *httptest.ResponseRecorder {
		body := map[string]any{"item_id": item.ID, "type": kind, "quantity": 1, "login": who.Login}
		if kind == "loan" || kind == "return" {
			body["activity_id"] = activityID
		}
		return doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
	}
	for _, kind := range []string{"loan", "rental"} {
		if rec := move(kind, member); rec.Code != http.StatusCreated {
			t.Errorf("%s to a member: %d %s", kind, rec.Code, rec.Body.String())
		}
		for name, who := range map[string]testutil.TestUser{"a former member": former, "somebody who never was a member": never} {
			if rec := move(kind, who); rec.Code != http.StatusConflict || errorCode(t, rec) != "recipient_not_member" {
				t.Errorf("%s to %s: %d %s", kind, name, rec.Code, rec.Body.String())
			}
		}
	}
	for name, who := range map[string]testutil.TestUser{"a member": member, "a former member": former} {
		if rec := move("sale", who); rec.Code != http.StatusCreated {
			t.Errorf("sale to %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := move("sale", never); rec.Code != http.StatusConflict || errorCode(t, rec) != "recipient_not_member_or_former" {
		t.Errorf("sale to somebody who never was a member: %d %s", rec.Code, rec.Body.String())
	}
}

// Brand, model and code are optional for general material (required for ropes and bags), but a model is the
// model of a brand: it cannot be given without one. Blank texts count as not given.
func TestHandlerModelNeedsABrand(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardToken := tokenFor(t, authn, 3, "board")

	body := func(extra map[string]any) map[string]any {
		b := generalItemBody(t, tx, 1)
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	// General material may have a brand, a model and a code, or any of them... except a model alone.
	for name, extra := range map[string]map[string]any{
		"nothing":           {},
		"a brand":           {"brand": "Petzl"},
		"a brand and model": {"brand": "Petzl", "model": "Vertex"},
		"all three":         {"brand": "Petzl", "model": "Vertex", "code": "H-1"},
		"a blank model":     {"brand": "Petzl", "model": "   "},
	} {
		if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, body(extra)); rec.Code != http.StatusCreated {
			t.Errorf("general material with %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, extra := range map[string]map[string]any{
		"a model alone":                     {"model": "Vertex"},
		"a model and a blank brand":         {"model": "Vertex", "brand": "  "},
		"a model and an empty-string brand": {"model": "Vertex", "brand": ""},
	} {
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, body(extra))
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "model_without_brand" {
			t.Errorf("general material with %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// Editing follows the same rule, and a blank value is stored as not given.
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, body(map[string]any{"brand": "Petzl", "model": "Vertex"}))
	var item material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/material/items/%d", item.ID)
	edit := func(extra map[string]any) *httptest.ResponseRecorder {
		b := map[string]any{"description": "Casco", "type_id": testutil.MaterialTypeID(t, tx, "Mosquetones")}
		for k, v := range extra {
			b[k] = v
		}
		return doRequest(mux, http.MethodPatch, path, boardToken, b)
	}
	if rec := edit(map[string]any{"model": "Vertex"}); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "model_without_brand" {
		t.Errorf("editing to a model without a brand: %d %s", rec.Code, rec.Body.String())
	}
	rec = edit(map[string]any{"brand": "Edelrid", "model": ""})
	var got material.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.Model != nil || got.Brand == nil || *got.Brand != "Edelrid" {
		t.Errorf("editing to a brand and a blank model: %d %s", rec.Code, rec.Body.String())
	}

	// A rope or a bag still needs all three.
	rope := map[string]any{
		"type_id": testutil.MaterialTypeID(t, tx, "Cuerda"), "brand": "Beal", "model": "Karma", "code": "C-9", "rope_type": "caving",
		"diameter_mm": 9.0, "manufacture_year": 2020, "length_m": 50,
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, rope); rec.Code != http.StatusCreated {
		t.Fatalf("rope: %d %s", rec.Code, rec.Body.String())
	}
	for _, missing := range []string{"brand", "model", "code"} {
		r := map[string]any{}
		for k, v := range rope {
			r[k] = v
		}
		r["code"] = "C-10"
		r[missing] = "  "
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a rope with a blank %s: %d %s", missing, rec.Code, rec.Body.String())
		}
	}
}

// Changing the list of types is for admin alone (the page /admin); reading it stays with
// admin, board and material.
func TestHandlerTypesAreManagedByAdminOnly(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")
	boardToken := tokenFor(t, authn, 2, "board")

	for name, token := range map[string]string{"board": boardToken, "nobody": ""} {
		if rec := doRequest(mux, http.MethodPost, "/api/v1/material/types", token, map[string]any{"name": "ZZ"}); rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("expected %s to be refused from creating a type, got %d", name, rec.Code)
		}
	}

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/types", adminToken, map[string]any{"name": "  Cintas  "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created material.MaterialType
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "Cintas" {
		t.Errorf("expected the name trimmed, got %q", created.Name)
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/types", adminToken, map[string]any{"name": "Cintas"}); rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate_type" {
		t.Errorf("expected 409 duplicate_type, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/types", adminToken, map[string]any{"name": "   "}); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a blank name, got %d", rec.Code)
	}

	path := fmt.Sprintf("/api/v1/material/types/%d", created.ID)
	if rec := doRequest(mux, http.MethodPatch, path, boardToken, map[string]any{"name": "Cintas planas"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from renaming, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, path, adminToken, map[string]any{"name": "Cintas planas"}); rec.Code != http.StatusOK {
		t.Errorf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPatch, "/api/v1/material/types/999999", adminToken, map[string]any{"name": "X"}); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 renaming a missing type, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path, adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 deleting it twice, got %d", rec.Code)
	}
}

func TestHandlerCannotDeleteATypeInUse(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	if rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", adminToken, generalItemBody(t, tx, 1)); rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", rec.Code, rec.Body.String())
	}
	path := fmt.Sprintf("/api/v1/material/types/%d", testutil.MaterialTypeID(t, tx, "Mosquetones"))
	rec := doRequest(mux, http.MethodDelete, path, adminToken, nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "type_in_use" {
		t.Errorf("expected 409 type_in_use, got %d %s", rec.Code, rec.Body.String())
	}
	// And the rest of the test's transaction still works after the refusal.
	if rec := doRequest(mux, http.MethodGet, "/api/v1/material/types", adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("list after the refusal: %d", rec.Code)
	}
}

// An item can be deleted, by admin alone, while it has no history beyond the acquisition
// it was created with (which goes with it); any other movement makes the delete refuse.
func TestHandlerDeleteItem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardUser := testutil.InsertUser(t, tx, "") // a real person: a movement records who made it
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	materialMemberToken := tokenFor(t, authn, 3, "material", "member")
	adminToken := tokenFor(t, authn, 4, "admin")

	create := func() material.Item {
		t.Helper()
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 3))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var item material.Item
		_ = json.Unmarshal(rec.Body.Bytes(), &item)
		return item
	}
	path := func(item material.Item) string { return fmt.Sprintf("/api/v1/material/items/%d", item.ID) }

	item := create()
	for name, token := range map[string]string{"a member": memberToken, "board": boardToken, "material": materialMemberToken} {
		if rec := doRequest(mux, http.MethodDelete, path(item), token, nil); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden from deleting an item, got %d", name, rec.Code)
		}
	}
	if rec := doRequest(mux, http.MethodDelete, path(item), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path(item), adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("deleting it twice: %d", rec.Code)
	}
	var left int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM material_movements WHERE item_id = $1`, item.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("expected its acquisition to go with it, %d left (%v)", left, err)
	}
	if rec := doRequest(mux, http.MethodDelete, "/api/v1/material/items/abc", adminToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", rec.Code)
	}

	// With any other movement it stays.
	kept := create()
	rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, map[string]any{
		"item_id": kept.ID, "type": "loss", "quantity": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("loss: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(mux, http.MethodDelete, path(kept), adminToken, nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "item_has_movements" {
		t.Errorf("an item with a movement: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/material/items", boardToken, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"id":%d,`, kept.ID)) {
		t.Errorf("expected the item to still be listed after the refusal")
	}
}

// A movement recorded by mistake can be deleted, by admin alone, as long as what is left makes sense:
// stock is what the movements add up to, so one that others depend on is refused.
func TestHandlerDeleteMovement(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	boardUser := testutil.InsertUser(t, tx, "") // a real person: a movement records who made it
	boardToken := tokenFor(t, authn, boardUser.ID, "board")
	materialToken := tokenFor(t, authn, 3, "material", "member")
	adminToken := tokenFor(t, authn, 4, "admin")
	activityID := testutil.InsertActivity(t, tx)

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", boardToken, generalItemBody(t, tx, 3))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: %d %s", rec.Code, rec.Body.String())
	}
	var item material.Item
	_ = json.Unmarshal(rec.Body.Bytes(), &item)

	record := func(body map[string]any) material.Movement {
		t.Helper()
		body["item_id"] = item.ID
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/movements", boardToken, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("record %v: %d %s", body, rec.Code, rec.Body.String())
		}
		var m material.Movement
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	path := func(m material.Movement) string { return fmt.Sprintf("/api/v1/material/movements/%d", m.ID) }
	current := func() material.Item {
		t.Helper()
		var got material.Item
		list := doRequest(mux, http.MethodGet, "/api/v1/material/items", boardToken, nil)
		var items []material.Item
		_ = json.Unmarshal(list.Body.Bytes(), &items)
		for _, i := range items {
			if i.ID == item.ID {
				got = i
			}
		}
		return got
	}
	movements := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/material/items/%d/movements", item.ID), boardToken, nil)
	var first []material.Movement
	_ = json.Unmarshal(movements.Body.Bytes(), &first)
	acquisition := first[0]

	// A sale of everything empties the item (status sold); deleting the acquisition would leave -3.
	sale := record(map[string]any{"type": "sale", "quantity": 3})
	if got := current(); got.Status != material.ItemSold || got.AvailableQuantity != 0 {
		t.Fatalf("after the sale: %+v", got)
	}
	for name, token := range map[string]string{"board": boardToken, "material": materialToken} {
		if rec := doRequest(mux, http.MethodDelete, path(sale), token, nil); rec.Code != http.StatusForbidden {
			t.Errorf("expected %s to be forbidden from deleting a movement, got %d", name, rec.Code)
		}
	}
	rec = doRequest(mux, http.MethodDelete, path(acquisition), adminToken, nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "movement_needed" {
		t.Errorf("the acquisition the sale depends on: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path(sale), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the sale: %d %s", rec.Code, rec.Body.String())
	}
	if got := current(); got.Status != material.ItemAvailable || got.AvailableQuantity != 3 || got.TotalQuantity != 3 {
		t.Errorf("after deleting the sale: %+v", got)
	}

	// A loan and its return: the loan cannot go while the return needs it; the return can, and then the
	// loan is out again.
	loan := record(map[string]any{"type": "loan", "quantity": 2, "activity_id": activityID})
	back := record(map[string]any{"type": "return", "quantity": 2, "activity_id": activityID})
	rec = doRequest(mux, http.MethodDelete, path(loan), adminToken, nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "movement_needed" {
		t.Errorf("the loan its return depends on: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path(back), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the return: %d %s", rec.Code, rec.Body.String())
	}
	if got := current(); got.OutstandingLoanQuantity != 2 || got.AvailableQuantity != 1 {
		t.Errorf("after deleting the return: %+v", got)
	}

	if rec := doRequest(mux, http.MethodDelete, path(back), adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("deleting it twice: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, "/api/v1/material/movements/abc", adminToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", rec.Code)
	}
}

package licenses_test

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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *memberships.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	licenses.NewHandler(licensesService, authenticator, usersService, nil).RegisterRoutes(mux)
	return mux, resolver, membershipsService, tx
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

// reset discards whatever's already been recorded — used between a
// test's setup calls (which fire their own notifications) and the
// actual action under test, so waitForMessage picks up that action's
// own message rather than an earlier setup one.
func (f *fakeNotifier) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
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

// newHandlerTestServerWithNotifier is newHandlerTestServer, but also
// wires a fakeNotifier so tests can assert on mutation-notification
// content.
func newHandlerTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *memberships.Service, pgx.Tx, *fakeNotifier) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	licenses.NewHandler(licensesService, authenticator, usersService, notifier).RegisterRoutes(mux)
	return mux, resolver, membershipsService, tx, notifier
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

// TestHandlerListTypesIsAdminOnly covers a deliberate exception to this
// codebase's usual pattern: most read-only catalogs (roles, the rest of
// this handler's own listByUser) are open to board too, but the license
// type catalog only exists to populate the "assign a license" form, so
// it's admin-only like create/update/delete.
func TestHandlerListTypesIsAdminOnly(t *testing.T) {
	mux, resolver, _, _ := newHandlerTestServer(t)

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodGet, "/api/v1/license-types", boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from listing license types, got %d: %s", rec.Code, rec.Body.String())
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodGet, "/api/v1/license-types", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected admin to list license types, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []licenses.LicenseType
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) == 0 {
		t.Error("expected at least the seeded license types")
	}
}

func TestHandlerCreateRequiresAdmin(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	boardToken := testutil.TokenFor(resolver, 1, "board")
	body := map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)}
	if rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from assigning a license, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateSucceedsForAnActiveMember is the happy path: the
// target has an open membership period covering issued_date (see
// Service.Create's own membership-date gate), so assigning a license
// works and the response is filled in with the login from the path.
func TestHandlerCreateSucceedsForAnActiveMember(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	body := map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)}
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var l licenses.License
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if l.UserLogin != target.Login {
		t.Errorf("expected user_login %q, got %q", target.Login, l.UserLogin)
	}
}

func TestHandlerCreateRejectsMissingLicenseType(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	body := map[string]any{"issued_date": "2026-01-01"}
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 missing_fields, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateRejectsAnIssuedDateOutsideAnyMembership covers
// Service.Create's own gate: the target here has never been a member at
// all, so no issued_date can ever satisfy WasMemberOn.
func TestHandlerCreateRejectsAnIssuedDateOutsideAnyMembership(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	body := map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)}
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken, body)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 not_active_member, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateOtraRequiresOtherDetail(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	otraID := testutil.LicenseTypeID(t, tx, "Otra")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	body := map[string]any{"license_type_id": otraID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)}
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_other_detail, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateRejectsOtherDetailForANonOtraType is the flip side of
// the same rule: other_detail only makes sense for the catalog's "Otra"
// escape hatch, so giving one for any other type is also rejected.
func TestHandlerCreateRejectsOtherDetailForANonOtraType(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	detail := "unexpected"
	body := map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "other_detail": detail, "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)}
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_other_detail, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateRequiresAdmin(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	path := fmt.Sprintf("/api/v1/licenses/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, path, boardToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a license, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateNotFound(t *testing.T) {
	mux, resolver, _, tx := newHandlerTestServer(t)
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")

	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodPatch, "/api/v1/licenses/999999999", adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerUpdateDoesNotRecheckMembershipStatus is the documented,
// deliberate asymmetry with Create (see Service.Update's own doc
// comment): correcting a license someone already has must keep working
// even once they're no longer a member, and even for an issued_date that
// falls outside every membership period on file — that gate only exists
// to stop assigning a brand new license, not to keep an existing one
// pinned to a still-valid date.
func TestHandlerUpdateDoesNotRecheckMembershipStatus(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 1999-01-01 falls well outside the person's only membership period
	// (starting 2020-01-01) — Create would reject this date outright,
	// but Update must accept it.
	path := fmt.Sprintf("/api/v1/licenses/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, path, adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "1999-01-01", "year": 1999})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 despite the issued_date falling outside any membership period, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteRequiresAdmin(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	path := fmt.Sprintf("/api/v1/licenses/%d", created.ID)
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting a license, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doRequest(mux, http.MethodDelete, path, adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteNotFound(t *testing.T) {
	mux, resolver, _, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodDelete, "/api/v1/licenses/999999999", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerListByUserSelfVersusOthers covers the same
// self-or-admin/board pattern userfiles/memberships/board all share:
// the target person can see their own license history, a different
// plain member can't see anyone else's, and admin/board can see anyone's.
func TestHandlerListByUserSelfVersusOthers(t *testing.T) {
	mux, resolver, memberships_, tx := newHandlerTestServer(t)
	ctx := context.Background()
	target := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	if _, err := memberships_.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})

	path := fmt.Sprintf("/api/v1/users/%s/licenses", target.Login)

	selfToken := testutil.TokenFor(resolver, target.ID, "member")
	if rec := doRequest(mux, http.MethodGet, path, selfToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected the target to view their own licenses, got %d: %s", rec.Code, rec.Body.String())
	}

	otherToken := testutil.TokenFor(resolver, other.ID, "member")
	if rec := doRequest(mux, http.MethodGet, path, otherToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a different plain member to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodGet, path, boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to view anyone's licenses, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateNotifiesTelegramWithActorAndTypeAndTarget(t *testing.T) {
	mux, resolver, membershipsService, tx, notifier := newHandlerTestServerWithNotifier(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "A BASICA") {
		t.Errorf("expected the notification to name the license type, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff covers the core of the
// feature: seeing exactly what changed (here, the issued_date), not
// just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, resolver, membershipsService, tx, notifier := newHandlerTestServerWithNotifier(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // the create notification from setup above
	notifier.reset()

	path := fmt.Sprintf("/api/v1/licenses/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, path, adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-06-01", "year": 2026})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "IssuedDate: 2026-01-01 → 2026-06-01") {
		t.Errorf("expected the notification to show the issued_date change, got %q", msg)
	}
}

func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, resolver, membershipsService, tx, notifier := newHandlerTestServerWithNotifier(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // the create notification from setup above
	notifier.reset()

	path := fmt.Sprintf("/api/v1/licenses/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, path, adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

func TestHandlerDeleteNotifiesTelegramWithActorAndTarget(t *testing.T) {
	mux, resolver, membershipsService, tx, notifier := newHandlerTestServerWithNotifier(t)
	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: date("2020-01-01")}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	typeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")
	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/licenses", target.Login), adminToken,
		map[string]any{"license_type_id": typeID, "issued_date": "2026-01-01", "year": 2026, "transaction_id": testutil.InsertTransaction(t, tx)})
	var created licenses.License
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // the create notification from setup above
	notifier.reset()

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/licenses/%d", created.ID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
}

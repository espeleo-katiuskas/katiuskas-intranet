package roles_test

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
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

// newRolesTestServer returns the transaction alongside the server so test
// setup (inserting users, looking up role ids) happens in the exact same
// transaction the handler queries — a separate transaction wouldn't see
// data this one hasn't committed yet.
func newRolesTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *roles.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	service := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), service, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	roles.NewHandler(service, authenticator, usersService, nil).RegisterRoutes(mux)
	return mux, resolver, service, tx
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

// newRolesTestServerWithNotifier is newRolesTestServer, but also wires
// a fakeNotifier so tests can assert on mutation-notification content.
func newRolesTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *roles.Service, pgx.Tx, *fakeNotifier) {
	tx := testutil.NewTx(t)
	service := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), service, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	roles.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
	return mux, resolver, service, tx, notifier
}

func rolesToken(t *testing.T, resolver testutil.FakeResolver, id int64, roleNames ...string) string {
	t.Helper()
	return testutil.TokenFor(resolver, id, roleNames...)
}

func rolesDoRequest(mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
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

func rolesErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestHandlerCreateAndDeleteRequireAdminNotJustBoard(t *testing.T) {
	mux, authn, _, _ := newRolesTestServer(t)
	boardToken := rolesToken(t, authn, 1, "board")

	rec := rolesDoRequest(mux, http.MethodPost, "/api/v1/roles", boardToken, map[string]string{"name": "material"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a role, got %d", rec.Code)
	}
}

func TestHandlerListAllowsBoard(t *testing.T) {
	mux, authn, _, _ := newRolesTestServer(t)
	boardToken := rolesToken(t, authn, 1, "board")

	if rec := rolesDoRequest(mux, http.MethodGet, "/api/v1/roles", boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to be able to list roles, got %d", rec.Code)
	}
}

// The catalog itself (unlike assigning/revoking) is just organizational
// labels — open to a plain member too, so their own "Cargo" column on
// /members can tell an automatic role apart from a real one.
func TestHandlerListAllowsMember(t *testing.T) {
	mux, authn, _, _ := newRolesTestServer(t)
	memberToken := rolesToken(t, authn, 1, "member")

	if rec := rolesDoRequest(mux, http.MethodGet, "/api/v1/roles", memberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a plain member to be able to list roles, got %d", rec.Code)
	}
}

// Board lost the ability to modify a person's data entirely, role
// assignment included — granting or revoking any role, not just admin,
// is admin-only now.
func TestHandlerBoardCannotGrantOrRevokeAnyRole(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	materialRoleID := testutil.RoleID(t, tx, "material")

	boardToken := rolesToken(t, authn, 1, "board")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/roles", target.Login), boardToken,
		map[string]int64{"role_id": materialRoleID})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board granting a role to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/roles/%d", target.Login, materialRoleID), boardToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board revoking a role to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerAdminCanGrantNonAdminRoles(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	materialRoleID := testutil.RoleID(t, tx, "material")

	adminToken := rolesToken(t, authn, 1, "admin")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/roles", target.Login), adminToken,
		map[string]int64{"role_id": materialRoleID})
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to grant a non-admin role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerAssignAndRemoveRejectAutomaticRoles(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	memberRoleID := testutil.RoleID(t, tx, "member")

	adminToken := rolesToken(t, authn, 1, "admin")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/roles", target.Login), adminToken,
		map[string]int64{"role_id": memberRoleID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 granting an automatic role, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "automatic_role" {
		t.Errorf("expected error code automatic_role, got %q", code)
	}

	rec = rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/roles/%d", target.Login, memberRoleID), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 revoking an automatic role, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "automatic_role" {
		t.Errorf("expected error code automatic_role, got %q", code)
	}
}

func TestHandlerAdminCanGrantTheAdminRole(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminRoleID := testutil.RoleID(t, tx, "admin")

	adminToken := rolesToken(t, authn, 1, "admin")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/roles", target.Login), adminToken,
		map[string]int64{"role_id": adminRoleID})
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to be able to grant admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The service-level rule (see TestRemoveBlocksAdminRemovingOwnAdminRole
// in service_test.go) end-to-end: an admin hitting DELETE on their own
// admin badge gets a specific 409, not a silent success or a 403.
func TestHandlerRemoveRefusesSelfAdminRemoval(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	actor := testutil.InsertUser(t, tx, "")
	adminRoleID := testutil.RoleID(t, tx, "admin")
	adminToken := rolesToken(t, authn, actor.ID, "admin")

	rec := rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/roles/%d", actor.Login, adminRoleID), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 removing your own admin role, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "self_admin_removal" {
		t.Errorf("expected error code self_admin_removal, got %q", code)
	}
}

func TestHandlerDeleteRefusesSystemRole(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	adminRoleID := testutil.RoleID(t, tx, "admin")

	adminToken := rolesToken(t, authn, 1, "admin")
	rec := rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/roles/%d", adminRoleID), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "system_role" {
		t.Errorf("expected error code system_role, got %q", code)
	}
}

func TestHandlerCreateNotifiesTelegram(t *testing.T) {
	mux, authn, _, tx, notifier := newRolesTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	adminToken := rolesToken(t, authn, actor.ID, "admin")

	rec := rolesDoRequest(mux, http.MethodPost, "/api/v1/roles", adminToken,
		map[string]string{"name": "vocal"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, "vocal") {
		t.Errorf("expected the notification to name the actor and the new role, got %q", msg)
	}
}

func TestHandlerDeleteNotifiesTelegramWithRoleName(t *testing.T) {
	mux, authn, service, tx, notifier := newRolesTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	adminToken := rolesToken(t, authn, actor.ID, "admin")

	created, err := service.Create(context.Background(), roles.CreateInput{Name: "logistica"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	rec := rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/roles/%d", created.ID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, "logistica") {
		t.Errorf("expected the notification to name the actor and the deleted role, got %q", msg)
	}
}

func TestHandlerAssignNotifiesTelegramWithRoleAndTarget(t *testing.T) {
	mux, authn, _, tx, notifier := newRolesTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	materialRoleID := testutil.RoleID(t, tx, "material")
	adminToken := rolesToken(t, authn, actor.ID, "admin")

	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/roles", target.Login), adminToken,
		map[string]int64{"role_id": materialRoleID})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, "material") || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor, role, and target, got %q", msg)
	}
}

func TestHandlerRemoveNotifiesTelegramWithRoleAndTarget(t *testing.T) {
	mux, authn, service, tx, notifier := newRolesTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	materialRoleID := testutil.RoleID(t, tx, "material")
	if err := service.Assign(context.Background(), target.ID, materialRoleID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	adminToken := rolesToken(t, authn, actor.ID, "admin")

	rec := rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s/roles/%d", target.Login, materialRoleID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, "material") || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor, role, and target, got %q", msg)
	}
}

package roles_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
)

// newRolesTestServer returns the transaction alongside the server so test
// setup (inserting users, looking up role ids) happens in the exact same
// transaction the handler queries — a separate transaction wouldn't see
// data this one hasn't committed yet.
func newRolesTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *roles.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	service := roles.NewService(roles.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	roles.NewHandler(service, authenticator).RegisterRoutes(mux)
	return mux, resolver, service, tx
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

func TestHandlerOnlyAdminCanGrantTheAdminRole(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminRoleID := testutil.RoleID(t, tx, "admin")

	boardToken := rolesToken(t, authn, 1, "board")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/roles", target.ID), boardToken,
		map[string]int64{"role_id": adminRoleID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected board granting admin to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "forbidden" {
		t.Errorf("expected error code forbidden, got %q", code)
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
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/roles", target.ID), boardToken,
		map[string]int64{"role_id": materialRoleID})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board granting a role to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d/roles/%d", target.ID, materialRoleID), boardToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board revoking a role to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerAdminCanGrantNonAdminRoles(t *testing.T) {
	mux, authn, _, tx := newRolesTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	materialRoleID := testutil.RoleID(t, tx, "material")

	adminToken := rolesToken(t, authn, 1, "admin")
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/roles", target.ID), adminToken,
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
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/roles", target.ID), adminToken,
		map[string]int64{"role_id": memberRoleID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 granting an automatic role, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := rolesErrorCode(t, rec); code != "automatic_role" {
		t.Errorf("expected error code automatic_role, got %q", code)
	}

	rec = rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d/roles/%d", target.ID, memberRoleID), adminToken, nil)
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
	rec := rolesDoRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%d/roles", target.ID), adminToken,
		map[string]int64{"role_id": adminRoleID})
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to be able to grant admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerOnlyAdminCanRevokeTheAdminRole(t *testing.T) {
	mux, authn, service, tx := newRolesTestServer(t)
	ctx := context.Background()
	adminRoleID := testutil.RoleID(t, tx, "admin")

	holder := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	if err := service.Assign(ctx, holder.ID, adminRoleID); err != nil {
		t.Fatalf("assign admin to holder: %v", err)
	}
	if err := service.Assign(ctx, other.ID, adminRoleID); err != nil {
		t.Fatalf("assign admin to other (avoids tripping the last-admin guard): %v", err)
	}

	boardToken := rolesToken(t, authn, 1, "board")
	rec := rolesDoRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d/roles/%d", holder.ID, adminRoleID), boardToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected board revoking admin to be forbidden, got %d: %s", rec.Code, rec.Body.String())
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

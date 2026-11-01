package tokens_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

// overrideEnv is a server whose sessions are real (tokens.Service resolves them), with two probe
// routes that tell which roles the session acts with.
type overrideEnv struct {
	mux      *http.ServeMux
	service  *tokens.Service
	notifier *fakeNotifier
	admin    int64
	adminTok string
	otherTok string
	repo     *tokens.Repository
	tx       pgx.Tx
	roles    *roles.Service
}

func newOverrideEnv(t *testing.T) *overrideEnv {
	t.Helper()
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	repo := tokens.NewRepository(tx)
	service := tokens.NewService(repo, rolesService)
	authenticator := auth.NewAuthenticator(service)
	notifier := &fakeNotifier{}
	mux := http.NewServeMux()
	tokens.NewHandler(service, authenticator, usersService, time.Hour, notifier, "https://intranet.example.test").RegisterRoutes(mux)
	mux.Handle("GET /probe/admin", authenticator.Protect(func(w http.ResponseWriter, r *http.Request) {}, "admin"))
	mux.Handle("GET /probe/any", authenticator.Protect(func(w http.ResponseWriter, r *http.Request) {}))
	mux.Handle("GET /probe/roles", authenticator.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFromContext(r.Context())
		w.Write([]byte(strings.Join(u.Roles, ",") + "|" + strings.Join(u.RealRoles, ",")))
	})))

	ctx := context.Background()
	admin := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	if err := rolesService.AssignByName(ctx, admin.ID, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := rolesService.AssignByName(ctx, other.ID, []string{"material"}); err != nil {
		t.Fatal(err)
	}
	adminTok, err := service.Issue(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherTok, err := service.Issue(ctx, other.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &overrideEnv{mux: mux, service: service, notifier: notifier, admin: admin.ID, adminTok: adminTok, otherTok: otherTok, repo: repo, tx: tx, roles: rolesService}
}

func (e *overrideEnv) status(method, path, token string, body any) int {
	return doRequest(e.mux, method, path, token, body).Code
}

func TestRoleOverrideActsWithOtherRolesAndPutsThemBack(t *testing.T) {
	e := newOverrideEnv(t)
	if e.status("GET", "/probe/admin", e.adminTok, nil) != http.StatusOK {
		t.Fatal("the admin starts as admin")
	}

	rec := doRequest(e.mux, "PUT", "/api/v1/auth/role-override", e.adminTok, map[string]any{"roles": []string{"member", "money", "member"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"roles":["member","money"]`) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	// Acting with other roles is not announced on Telegram.
	time.Sleep(20 * time.Millisecond)
	e.notifier.mu.Lock()
	sent := len(e.notifier.sent)
	e.notifier.mu.Unlock()
	if sent != 0 {
		t.Fatalf("expected no notice, got %d", sent)
	}
	// Acting as member + money: no admin routes, the ordinary ones work, and the real roles are kept.
	if got := e.status("GET", "/probe/admin", e.adminTok, nil); got != http.StatusForbidden {
		t.Fatalf("admin route as member: %d", got)
	}
	if got := e.status("GET", "/probe/any", e.adminTok, nil); got != http.StatusOK {
		t.Fatalf("baseline route as member: %d", got)
	}
	body := doRequest(e.mux, "GET", "/probe/roles", e.adminTok, nil).Body.String()
	if body != "member,money|admin" {
		t.Fatalf("roles seen: %q", body)
	}

	// Changing it from inside the override works (the person is an admin for real).
	if got := e.status("PUT", "/api/v1/auth/role-override", e.adminTok, map[string]any{"roles": []string{"board"}}); got != http.StatusOK {
		t.Fatalf("change: %d", got)
	}
	if body := doRequest(e.mux, "GET", "/probe/roles", e.adminTok, nil).Body.String(); body != "board|admin" {
		t.Fatalf("roles after the change: %q", body)
	}

	// Putting them back.
	if got := e.status("DELETE", "/api/v1/auth/role-override", e.adminTok, nil); got != http.StatusNoContent {
		t.Fatalf("clear: %d", got)
	}
	if got := e.status("GET", "/probe/admin", e.adminTok, nil); got != http.StatusOK {
		t.Fatalf("admin again: %d", got)
	}
	// And doing it again is not an error.
	if got := e.status("DELETE", "/api/v1/auth/role-override", e.adminTok, nil); got != http.StatusNoContent {
		t.Fatalf("clear twice: %d", got)
	}
}

func TestRoleOverrideEndsOnItsOwn(t *testing.T) {
	e := newOverrideEnv(t)
	ctx := context.Background()
	if _, _, err := e.service.SetRoleOverride(ctx, e.adminTok, []string{"member"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := e.status("GET", "/probe/admin", e.adminTok, nil); got != http.StatusForbidden {
		t.Fatalf("while it lasts: %d", got)
	}
	// An override that is over is as if there were none.
	if err := e.repo.SetRoleOverride(ctx, auth.HashToken(e.adminTok), []string{"member"}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := e.status("GET", "/probe/admin", e.adminTok, nil); got != http.StatusOK {
		t.Fatalf("after it ends: %d", got)
	}
	if body := doRequest(e.mux, "GET", "/probe/roles", e.adminTok, nil).Body.String(); body != "admin|admin" {
		t.Fatalf("roles after it ends: %q", body)
	}
}

func TestRoleOverrideValidationAndPermissions(t *testing.T) {
	e := newOverrideEnv(t)
	put := func(token string, roles ...string) *httptest.ResponseRecorder {
		return doRequest(e.mux, "PUT", "/api/v1/auth/role-override", token, map[string]any{"roles": roles})
	}
	// Only a real admin.
	if rec := put(e.otherTok, "admin"); rec.Code != http.StatusForbidden {
		t.Fatalf("not an admin: %d", rec.Code)
	}
	if got := e.status("DELETE", "/api/v1/auth/role-override", e.otherTok, nil); got != http.StatusForbidden {
		t.Fatalf("not an admin, clear: %d", got)
	}
	if got := e.status("PUT", "/api/v1/auth/role-override", "", nil); got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}
	for _, c := range []struct {
		roles []string
		code  string
	}{
		{nil, "invalid_roles"},
		{[]string{"nobody"}, "unknown_role"},
		{[]string{"material"}, "no_baseline_role"},
		{[]string{"money", "former_member"}, "no_baseline_role"},
	} {
		rec := put(e.adminTok, c.roles...)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
			t.Fatalf("roles %v: %d %s", c.roles, rec.Code, rec.Body.String())
		}
	}
	if got := e.status("GET", "/probe/admin", e.adminTok, nil); got != http.StatusOK {
		t.Fatal("a refused request must not change the roles")
	}
	if got := e.status("PUT", "/api/v1/auth/role-override", e.adminTok, "not json"); got != http.StatusBadRequest {
		t.Fatalf("malformed: %d", got)
	}
}

func TestAnImpersonationCannotChangeRoles(t *testing.T) {
	e := newOverrideEnv(t)
	ctx := context.Background()
	// An admin impersonating another admin: the session is somebody else's, even if that somebody is
	// an admin for real.
	target := testutil.InsertUser(t, e.tx, "")
	if err := e.roles.AssignByName(ctx, target.ID, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	raw, err := e.service.Impersonate(ctx, e.admin, target.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rec := doRequest(e.mux, "PUT", "/api/v1/auth/role-override", raw, map[string]any{"roles": []string{"member"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"impersonating"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

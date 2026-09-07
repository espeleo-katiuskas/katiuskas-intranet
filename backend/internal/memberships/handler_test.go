package memberships_test

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

func newHandlerTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	memberships.NewHandler(membershipsService, authenticator, usersService, nil).RegisterRoutes(mux)
	return mux, resolver, tx
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
// still be in flight by then). Pops the message it returns off the
// front of the queue — a test whose own setup (e.g. createMembership,
// which goes through the real HTTP handler) triggers a notification of
// its own calls this once to consume and discard it before checking
// the one the actual action under test produces.
func (f *fakeNotifier) waitForMessage(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.sent = f.sent[1:]
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
func newHandlerTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx, *fakeNotifier) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	memberships.NewHandler(membershipsService, authenticator, usersService, notifier).RegisterRoutes(mux)
	return mux, resolver, tx, notifier
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

// createMembership is a small helper the leave tests reuse: they need a
// real membership row to hang a leave off of, and always via the admin
// path (leave-specific role checks are exercised separately).
func createMembership(t *testing.T, mux *http.ServeMux, resolver testutil.FakeResolver, login, startDate string) memberships.Membership {
	t.Helper()
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/memberships", login), adminToken,
		map[string]string{"start_date": startDate})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup create membership: %d %s", rec.Code, rec.Body.String())
	}
	var m memberships.Membership
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode membership: %v", err)
	}
	return m
}

func TestHandlerListActiveRequiresAdminOrBoard(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)

	memberToken := testutil.TokenFor(resolver, 1, "member")
	if rec := doRequest(mux, http.MethodGet, "/api/v1/memberships", memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from listing active memberships, got %d", rec.Code)
	}

	for _, role := range []string{"admin", "board"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/memberships", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list active memberships, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

// TestHandlerCreateRequiresAdminNotBoard covers the CLAUDE.md rule
// directly: board can read every membership period but can't start,
// correct, or end one — that write access is admin-only now, same as
// the rest of a person's own data.
func TestHandlerCreateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")

	boardToken := testutil.TokenFor(resolver, 1, "board")
	path := fmt.Sprintf("/api/v1/users/%s/memberships", target.Login)
	body := map[string]string{"start_date": "2020-01-01"}
	if rec := doRequest(mux, http.MethodPost, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a membership, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodPost, path, adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created memberships.Membership
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.UserLogin != target.Login {
		t.Errorf("expected user_login %q, got %q", target.Login, created.UserLogin)
	}
}

func TestHandlerCreateRejectsMalformedStartDate(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	path := fmt.Sprintf("/api/v1/users/%s/memberships", target.Login)
	rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"start_date": "not-a-date"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_date, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateRejectsAnOverlappingPeriod exercises the exclusion
// constraint memberships.Repository.Create translates into ErrOverlap:
// an open-ended period (no end_date) overlaps any later start date for
// the same person (see repository_test.go's own
// TestCreateRejectsOverlappingPeriod).
func TestHandlerCreateRejectsAnOverlappingPeriod(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/memberships", target.Login)

	first := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"start_date": "2020-01-01"})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected the first period to be created, got %d: %s", first.Code, first.Body.String())
	}

	second := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"start_date": "2021-01-01"})
	if second.Code != http.StatusConflict {
		t.Errorf("expected 409 overlapping_period for a second open period, got %d: %s", second.Code, second.Body.String())
	}
}

func TestHandlerUpdateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")

	updatePath := fmt.Sprintf("/api/v1/memberships/%d", m.ID)
	body := map[string]string{"start_date": "2020-01-01", "end_date": "2022-01-01"}

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodPatch, updatePath, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a membership, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated memberships.Membership
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.EndDate == nil {
		t.Errorf("expected end_date to be set, got %+v", updated)
	}
	if updated.UserLogin != target.Login {
		t.Errorf("expected user_login %q resolved back from the membership's user id, got %q", target.Login, updated.UserLogin)
	}
}

func TestHandlerUpdateRejectsNonNumericID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPatch, "/api/v1/memberships/not-a-number", adminToken,
		map[string]string{"start_date": "2020-01-01"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPatch, "/api/v1/memberships/999999", adminToken,
		map[string]string{"start_date": "2020-01-01"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerListByUserIsSelfOrPrivileged covers listByUser's own access
// check directly: a plain member may see their own membership history,
// admin/board may see anyone's, but a different plain member may see
// nobody else's.
func TestHandlerListByUserIsSelfOrPrivileged(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	path := fmt.Sprintf("/api/v1/users/%s/memberships", target.Login)

	selfToken := testutil.TokenFor(resolver, target.ID, "member")
	if rec := doRequest(mux, http.MethodGet, path, selfToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected the target themselves to view their own membership history, got %d: %s", rec.Code, rec.Body.String())
	}

	otherMemberToken := testutil.TokenFor(resolver, target.ID+1, "member")
	if rec := doRequest(mux, http.MethodGet, path, otherMemberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a different plain member to be forbidden, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, target.ID+2, "admin")
	if rec := doRequest(mux, http.MethodGet, path, adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected admin to view anyone's membership history, got %d: %s", rec.Code, rec.Body.String())
	}

	boardToken := testutil.TokenFor(resolver, target.ID+3, "board")
	if rec := doRequest(mux, http.MethodGet, path, boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to view anyone's membership history, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserUnknownLoginIsNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodGet, "/api/v1/users/no-such-login/memberships", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found for an unknown login, got %d", rec.Code)
	}
}

// TestHandlerListLeavesRequiresAdminOrBoard: leaves are readable by
// admin/board (same as the membership they belong to) but not by a
// plain member, even one on the RegisterRoutes baseline.
func TestHandlerListLeavesRequiresAdminOrBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")

	memberToken := testutil.TokenFor(resolver, 1, "member")
	path := fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID)
	if rec := doRequest(mux, http.MethodGet, path, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from listing leaves, got %d", rec.Code)
	}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodGet, path, boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to list leaves, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListLeavesRejectsNonNumericMembershipID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodGet, "/api/v1/memberships/not-a-number/leaves", adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateLeaveRequiresAdminNotBoard: unlike listLeaves, board
// can't create one — writing a leave is writing membership data, same
// as the period it belongs to.
func TestHandlerCreateLeaveRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	path := fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID)
	body := map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"}

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodPost, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a leave, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodPost, path, adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created memberships.Leave
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.MembershipID != m.ID {
		t.Errorf("expected membership_id %d, got %d", m.ID, created.MembershipID)
	}
}

func TestHandlerCreateLeaveRejectsNonNumericMembershipID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/memberships/not-a-number/leaves", adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateLeaveRejectsOutsideMembershipPeriod covers the one
// invariant this package enforces itself rather than leaning on a
// database constraint (see model.go's own package doc comment): a leave
// starting before its membership even began.
func TestHandlerCreateLeaveRejectsOutsideMembershipPeriod(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	path := fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID)
	rec := doRequest(mux, http.MethodPost, path, adminToken,
		map[string]string{"start_date": "2019-12-01", "end_date": "2020-01-15"})
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 outside_membership_period, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateLeaveRejectsOverlap(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID)

	first := doRequest(mux, http.MethodPost, path, adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected the first leave to be created, got %d: %s", first.Code, first.Body.String())
	}

	second := doRequest(mux, http.MethodPost, path, adminToken,
		map[string]string{"start_date": "2020-06-15", "end_date": "2020-07-15"})
	if second.Code != http.StatusConflict {
		t.Errorf("expected 409 overlapping_period, got %d: %s", second.Code, second.Body.String())
	}
}

func TestHandlerUpdateLeaveRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	created := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create leave: %d %s", created.Code, created.Body.String())
	}
	var leave memberships.Leave
	if err := json.Unmarshal(created.Body.Bytes(), &leave); err != nil {
		t.Fatalf("decode: %v", err)
	}

	updatePath := fmt.Sprintf("/api/v1/leaves/%d", leave.ID)
	body := map[string]string{"start_date": "2020-06-05", "end_date": "2020-07-05"}

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodPatch, updatePath, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a leave, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateLeaveNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodPatch, "/api/v1/leaves/999999", adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteLeaveRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	created := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create leave: %d %s", created.Code, created.Body.String())
	}
	var leave memberships.Leave
	if err := json.Unmarshal(created.Body.Bytes(), &leave); err != nil {
		t.Fatalf("decode: %v", err)
	}
	deletePath := fmt.Sprintf("/api/v1/leaves/%d", leave.ID)

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodDelete, deletePath, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting a leave, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodDelete, deletePath, adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteLeaveNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/leaves/999999", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateNotifiesTelegram(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/memberships", target.Login), adminToken,
		map[string]string{"start_date": "2020-01-01"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) || !strings.Contains(msg, "2020-01-01") {
		t.Errorf("expected the notification to name actor, target, and start date, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff is the core of the
// feature this whole notifier exists for: seeing exactly what changed,
// not just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	notifier.waitForMessage(t) // discard createMembership's own "alta" notification
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/memberships/%d", m.ID), adminToken,
		map[string]string{"start_date": "2020-01-01", "end_date": "2020-12-31", "end_reason": "voluntaria"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "EndDate: (vacío) → 2020-12-31") {
		t.Errorf("expected the notification to show the end_date change, got %q", msg)
	}
}

// TestHandlerUpdateWithNoActualChangeSendsNoNotification makes sure a
// PATCH that doesn't actually change anything (every field resent with
// its own current value) doesn't produce a misleading "edited" message
// with an empty diff.
func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	notifier.waitForMessage(t) // discard createMembership's own "alta" notification
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/memberships/%d", m.ID), adminToken,
		map[string]string{"start_date": "2020-01-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

func TestHandlerCreateLeaveNotifiesTelegram(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	notifier.waitForMessage(t) // discard createMembership's own "alta" notification
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor and target, got %q", msg)
	}
}

func TestHandlerUpdateLeaveNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	notifier.waitForMessage(t) // discard createMembership's own "alta" notification
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	created := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create leave: %d %s", created.Code, created.Body.String())
	}
	notifier.waitForMessage(t) // discard the leave-creation notification
	var leave memberships.Leave
	if err := json.Unmarshal(created.Body.Bytes(), &leave); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/leaves/%d", leave.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-07-15"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "EndDate: 2020-06-30 → 2020-07-15") {
		t.Errorf("expected the notification to show the end_date change, got %q", msg)
	}
}

func TestHandlerDeleteLeaveNotifiesTelegram(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	m := createMembership(t, mux, resolver, target.Login, "2020-01-01")
	notifier.waitForMessage(t) // discard createMembership's own "alta" notification
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	created := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/memberships/%d/leaves", m.ID), adminToken,
		map[string]string{"start_date": "2020-06-01", "end_date": "2020-06-30"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create leave: %d %s", created.Code, created.Body.String())
	}
	notifier.waitForMessage(t) // discard the leave-creation notification
	var leave memberships.Leave
	if err := json.Unmarshal(created.Body.Bytes(), &leave); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/leaves/%d", leave.ID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor and target, got %q", msg)
	}
}

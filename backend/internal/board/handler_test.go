package board_test

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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/board"
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
	service := board.NewService(board.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	board.NewHandler(service, authenticator, usersService, nil).RegisterRoutes(mux)
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
func newHandlerTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx, *fakeNotifier) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := board.NewService(board.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	board.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
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

func TestHandlerListActiveOpenToAnyBaselineRole(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)

	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(resolver, 1, role)
		if rec := doRequest(mux, http.MethodGet, "/api/v1/board-members", token, nil); rec.Code != http.StatusOK {
			t.Errorf("expected %s to list active board members, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

// TestHandlerCreateRequiresAdminNotBoard covers this permission tier
// directly: board can read everything here but can't write — changing
// who's on the Junta Directiva is person data, same as the rest of a
// profile, which board lost write access to entirely.
func TestHandlerCreateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")

	boardToken := testutil.TokenFor(resolver, 1, "board")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	if rec := doRequest(mux, http.MethodPost, path, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from creating a board term, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, 2, "admin")
	rec := doRequest(mux, http.MethodPost, path, adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created board.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.UserLogin != target.Login {
		t.Errorf("expected user_login %q, got %q", target.Login, created.UserLogin)
	}
	if created.Position != board.PositionSecretary {
		t.Errorf("expected position secretary, got %q", created.Position)
	}
}

func TestHandlerCreateRejectsMalformedStartDate(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "secretary", "start_date": "not-a-date"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_date, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandlerCreateRejectsAnOverlappingTerm exercises the exclusion
// constraint board.Repository.Create translates into ErrOverlap: nobody
// holds two board terms at once, regardless of position (see
// repository_test.go's own TestCreateRejectsOverlappingTerm).
func TestHandlerCreateRejectsAnOverlappingTerm(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)

	first := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "president", "start_date": "2020-01-01"})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected the first term to be created, got %d: %s", first.Code, first.Body.String())
	}

	second := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "treasurer", "start_date": "2021-01-01"})
	if second.Code != http.StatusConflict {
		t.Errorf("expected 409 overlapping_period for a second open term, got %d: %s", second.Code, second.Body.String())
	}
}

func TestHandlerUpdateRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	createPath := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	created := doRequest(mux, http.MethodPost, createPath, adminToken, map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create: %d %s", created.Code, created.Body.String())
	}
	var member board.Member
	if err := json.Unmarshal(created.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode: %v", err)
	}

	updatePath := fmt.Sprintf("/api/v1/board-members/%d", member.ID)
	body := map[string]string{"position": "secretary", "start_date": "2020-01-01", "end_date": "2022-01-01"}

	boardToken := testutil.TokenFor(resolver, 1, "board")
	if rec := doRequest(mux, http.MethodPatch, updatePath, boardToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from updating a board term, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated board.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.EndDate == nil {
		t.Errorf("expected end_date to be set, got %+v", updated)
	}
	if updated.UserLogin != target.Login {
		t.Errorf("expected user_login %q resolved back from the term's user id, got %q", target.Login, updated.UserLogin)
	}
}

func TestHandlerUpdateRejectsNonNumericID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	rec := doRequest(mux, http.MethodPatch, "/api/v1/board-members/not-a-number", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	rec := doRequest(mux, http.MethodPatch, "/api/v1/board-members/999999", adminToken, body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteTermRequiresAdminNotBoard(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/board-members", target.Login), adminToken,
		map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	var created board.Member
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	deletePath := fmt.Sprintf("/api/v1/board-members/%d", created.ID)

	boardToken := testutil.TokenFor(resolver, 2, "board")
	if rec := doRequest(mux, http.MethodDelete, deletePath, boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board to be forbidden from deleting a board term, got %d", rec.Code)
	}

	rec := doRequest(mux, http.MethodDelete, deletePath, adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	listRec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s/board-members", target.Login), adminToken, nil)
	var list []board.Member
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected the term to be gone, got %+v", list)
	}
}

func TestHandlerDeleteTermRejectsNonNumericID(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/board-members/not-a-number", adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteTermNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/board-members/999999", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteTermNotifiesTelegram(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	actor := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, actor.ID, "admin")

	createRec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/users/%s/board-members", target.Login), adminToken,
		map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	var created board.Member
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.reset() // discard the create's own notification

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/board-members/%d", created.ID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, actor.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name actor and target, got %q", msg)
	}
}

// TestHandlerListByUserIsSelfOrPrivileged covers listByUser's own
// access check directly: a plain member may see their own board
// history, admin/board may see anyone's, but a different plain member
// may see nobody else's.
func TestHandlerListByUserIsSelfOrPrivileged(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)

	selfToken := testutil.TokenFor(resolver, target.ID, "member")
	if rec := doRequest(mux, http.MethodGet, path, selfToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected the target themselves to view their own board history, got %d: %s", rec.Code, rec.Body.String())
	}

	otherMemberToken := testutil.TokenFor(resolver, target.ID+1, "member")
	if rec := doRequest(mux, http.MethodGet, path, otherMemberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a different plain member to be forbidden, got %d", rec.Code)
	}

	adminToken := testutil.TokenFor(resolver, target.ID+2, "admin")
	if rec := doRequest(mux, http.MethodGet, path, adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected admin to view anyone's board history, got %d: %s", rec.Code, rec.Body.String())
	}

	boardToken := testutil.TokenFor(resolver, target.ID+3, "board")
	if rec := doRequest(mux, http.MethodGet, path, boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected board to view anyone's board history, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserUnknownLoginIsNotFound(t *testing.T) {
	mux, resolver, _ := newHandlerTestServer(t)
	adminToken := testutil.TokenFor(resolver, 1, "admin")

	rec := doRequest(mux, http.MethodGet, "/api/v1/users/no-such-login/board-members", adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 not_found for an unknown login, got %d", rec.Code)
	}
}

func TestHandlerCreateNotifiesTelegramWithActorAndTarget(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	body := map[string]string{"position": "secretary", "start_date": "2020-01-01"}
	rec := doRequest(mux, http.MethodPost, path, adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "secretary") {
		t.Errorf("expected the notification to name the position, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff covers the core of the
// feature: seeing exactly what changed (here, ending the term), not
// just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	createPath := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	createRec := doRequest(mux, http.MethodPost, createPath, adminToken, map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	var created board.Member
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // the create notification from setup above
	notifier.reset()

	updatePath := fmt.Sprintf("/api/v1/board-members/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken,
		map[string]string{"position": "secretary", "start_date": "2020-01-01", "end_date": "2022-01-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "EndDate: (vacío) → 2022-01-01") {
		t.Errorf("expected the notification to show the end_date change, got %q", msg)
	}
}

func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, resolver, tx, notifier := newHandlerTestServerWithNotifier(t)
	admin := testutil.InsertUser(t, tx, "")
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, admin.ID, "admin")

	createPath := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)
	createRec := doRequest(mux, http.MethodPost, createPath, adminToken, map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	var created board.Member
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // the create notification from setup above
	notifier.reset()

	updatePath := fmt.Sprintf("/api/v1/board-members/%d", created.ID)
	rec := doRequest(mux, http.MethodPatch, updatePath, adminToken,
		map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

// A position that isn't one of the board_position enum values is a 400,
// not the 500 Postgres' enum error used to become — on create and update.
func TestHandlerRejectsAnUnknownPosition(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	target := testutil.InsertUser(t, tx, "")
	adminToken := testutil.TokenFor(resolver, 1, "admin")
	path := fmt.Sprintf("/api/v1/users/%s/board-members", target.Login)

	rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "presidente", "start_date": "2020-01-01"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_position on create, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"invalid_position"`) {
		t.Errorf("expected error code invalid_position, got %s", rec.Body.String())
	}

	created := doRequest(mux, http.MethodPost, path, adminToken, map[string]string{"position": "secretary", "start_date": "2020-01-01"})
	if created.Code != http.StatusCreated {
		t.Fatalf("expected the term to be created, got %d: %s", created.Code, created.Body.String())
	}
	var m struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/board-members/%d", m.ID), adminToken, map[string]string{"position": "", "start_date": "2020-01-01"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_position on update, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The history lists every term, past and present, to anybody with a baseline role, with names but
// without the contact data that the current board has.
func TestHandlerHistoryListsEveryTermWithoutContactData(t *testing.T) {
	mux, resolver, tx := newHandlerTestServer(t)
	past := testutil.InsertUser(t, tx, "")
	current := testutil.InsertUser(t, tx, "")
	ctx := context.Background()
	for _, row := range []struct {
		userID     int64
		position   string
		start, end any
	}{
		{past.ID, "president", "2018-01-01", "2020-12-31"},
		{current.ID, "president", "2021-01-01", nil},
		{past.ID, "treasurer", "2021-01-01", "2022-06-30"},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO board_members (user_id, position, start_date, end_date) VALUES ($1, $2, $3, $4)`,
			row.userID, row.position, row.start, row.end); err != nil {
			t.Fatalf("insert term: %v", err)
		}
	}

	for _, role := range []string{"admin", "board", "member"} {
		rec := doRequest(mux, http.MethodGet, "/api/v1/board-members/history", testutil.TokenFor(resolver, 1, role), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected %s to read the history, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
	rec := doRequest(mux, http.MethodGet, "/api/v1/board-members/history", testutil.TokenFor(resolver, 1, "member"), nil)
	var list []board.Member
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected the 3 terms, got %d: %s", len(list), rec.Body.String())
	}
	// Newest start first; the ended ones are there too, with their end date.
	if list[2].EndDate == nil || list[2].UserLogin != past.Login || list[2].Position != board.PositionPresident {
		t.Errorf("expected the oldest term last, ended and with its person: %+v", list[2])
	}
	if strings.Contains(rec.Body.String(), `"email"`) || strings.Contains(rec.Body.String(), `"phone"`) {
		t.Errorf("the history must not carry contact data: %s", rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/board-members/history", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected an unauthenticated request to be refused, got %d", rec.Code)
	}
}

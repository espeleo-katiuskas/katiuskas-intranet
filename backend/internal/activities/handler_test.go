package activities_test

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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

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
// still be in flight by then). It pops the oldest undelivered message
// rather than just peeking at it, so a test that triggers more than one
// notification (e.g. a create followed by an update) can call this
// once per notification, in order, instead of always seeing the first.
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

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	return newTestServerWithTx(t)
}

// newTestServerWithTx also returns the underlying transaction, for tests
// that need to insert real user rows to satisfy a foreign key (e.g.
// signing someone up as a participant, or being the activity's leader —
// now required on every activity).
func newTestServerWithTx(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := activities.NewService(activities.NewRepository(tx), licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	activities.NewHandler(service, authenticator, usersService, nil).RegisterRoutes(mux)
	return mux, resolver, tx
}

// newTestServerWithNotifier is newTestServerWithTx, but also wires a
// fakeNotifier so tests can assert on mutation-notification content —
// separate from newTestServerWithTx itself so the many tests that don't
// care about notifications aren't slowed down polling for one that'll
// never arrive.
func newTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx, *fakeNotifier) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	service := activities.NewService(activities.NewRepository(tx), licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	activities.NewHandler(service, authenticator, usersService, notifier).RegisterRoutes(mux)
	return mux, resolver, tx, notifier
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

func TestHandlerCreateIsOpenToAnyMember(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	body := map[string]any{
		// "social", not "caving": this test is about who may create an
		// activity, not about the federation requirement.
		"title": "Cena de fin de temporada", "type": "social", "start_date": "2026-09-15", "end_date": "2026-09-15",
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected a plain member to create an activity (201), got %d: %s", rec.Code, rec.Body.String())
	}

	if rec := doRequest(mux, http.MethodPost, "/api/v1/activities", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

func TestHandlerGet(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d", created.ID), memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID || got.Title != "A" {
		t.Errorf("expected the created activity back, got %+v", got)
	}
}

func TestHandlerGetNotFound(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/activities/999999", memberToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListActiveFiltersOutCompletedActivities(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	member := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, member.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "Planned", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/activities?status=active", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list active: %d: %s", rec.Code, rec.Body.String())
	}
	var active []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active activity, got %d: %+v", len(active), active)
	}

	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d", active[0].ID), adminToken, map[string]any{"status": "completed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/activities?status=active", memberToken, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected the now-completed activity to drop out of ?status=active, got %+v", active)
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/activities", memberToken, nil)
	var all []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected the unfiltered list to still include it, got %+v", all)
	}
}

func TestHandlerCreateWithParticipants(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	body := map[string]any{
		"title": "Cena de fin de temporada", "type": "social", "start_date": "2026-09-15", "end_date": "2026-09-15",
		"participant_logins": []string{},
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with an empty participant list, got %d: %s", rec.Code, rec.Body.String())
	}

	body["participant_logins"] = []string{"no-such-login"}
	rec = doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown participant, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_participant" {
		t.Errorf("expected error code unknown_participant, got %q", code)
	}
}

func TestHandlerJoinSelfVersusAddingSomeoneElse(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	member := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, member.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	participantsPath := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)

	// No body: signs up the caller.
	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected a member to sign themselves up (204), got %d: %s", rec.Code, rec.Body.String())
	}
	// A member naming someone else is forbidden.
	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, map[string]any{"login": other.Login}); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member adding someone else to be forbidden, got %d", rec.Code)
	}
	// admin/board can add anyone.
	if rec := doRequest(mux, http.MethodPost, participantsPath, adminToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add someone else (204), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserIsOpenToAnyBaselineUser(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertUser(t, tx, "")
	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	memberToken := tokenFor(t, authn, other.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"participant_logins": []string{person.Login},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}

	path := fmt.Sprintf("/api/v1/users/%s/activities", person.Login)
	// Not even admin/board — a plain member (who isn't the person in
	// question) can still read it, same as listing an activity's
	// participants requires nothing beyond the baseline.
	rec = doRequest(mux, http.MethodGet, path, memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 activity, got %d", len(list))
	}
}

func TestHandlerCreateSetsLeaderIDFromTheCaller(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"leader_id": 999999, // must be ignored: the organizer is always the caller
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.LeaderLogin != person.Login {
		t.Errorf("expected leader_login to be the caller (%q), got %q", person.Login, created.LeaderLogin)
	}
}

// Board (and admin) can name someone else as the organizer right at
// creation time instead of always leading it themselves.
func TestHandlerCreateAllowsBoardToChooseADifferentLeader(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	board := testutil.InsertUser(t, tx, "")
	organizer := testutil.InsertUser(t, tx, "")
	boardToken := tokenFor(t, authn, board.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", boardToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"leader_login": organizer.Login,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.LeaderLogin != organizer.Login {
		t.Errorf("expected leader_login %q, got %q", organizer.Login, created.LeaderLogin)
	}
}

// A plain member can't hand their own new activity off to someone else
// — leader_login is board/admin only.
func TestHandlerCreateRejectsMemberChoosingADifferentLeader(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"leader_login": other.Login,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a member choosing a different organizer, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateAndCancelAllowLeaderOrAdminOrBoardOnly(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	otherMember := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	otherMemberToken := tokenFor(t, authn, otherMember.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	if rec := doRequest(mux, http.MethodPatch, path, otherMemberToken, map[string]any{"title": "New"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected a non-leader member to be forbidden from updating an activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"title": "New"}); rec.Code != http.StatusOK {
		t.Errorf("expected the organizer to update their own activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPatch, path, adminToken, map[string]any{"title": "Newer"}); rec.Code != http.StatusOK {
		t.Errorf("expected an admin to update an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path, otherMemberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a non-leader member to be forbidden from cancelling an activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, path, leaderToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected the organizer to cancel their own activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateCanChangeType(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "training", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"type": "social"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update type: %d: %s", rec.Code, rec.Body.String())
	}
	var updated activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Type != activities.TypeSocial {
		t.Errorf("expected type to change to social, got %q", updated.Type)
	}
}

func TestHandlerUpdateCanChangeDates(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "training", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)

	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{
		"start_date": "2026-02-01", "end_date": "2026-02-03",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update dates: %d: %s", rec.Code, rec.Body.String())
	}
	var updated activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !updated.StartDate.Equal(date("2026-02-01")) || !updated.EndDate.Equal(date("2026-02-03")) {
		t.Errorf("expected updated dates, got start %v end %v", updated.StartDate, updated.EndDate)
	}

	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"start_date": "not-a-date"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a malformed start_date, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"end_date": "2026-01-01"})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected the DB CHECK constraint to reject end_date before start_date, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerJoinRequiresFederationForCavingAndCanyoning(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertUser(t, tx, "")
	unfederated := testutil.InsertUser(t, tx, "")
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	unfederatedToken := tokenFor(t, authn, unfederated.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "Sima", "type": "caving", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create caving activity: %d: %s", rec.Code, rec.Body.String())
	}
	var caving activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &caving); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/participants", caving.ID), unfederatedToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for an unfederated member joining a caving activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_federated" {
		t.Errorf("expected error code not_federated, got %q", code)
	}

	rec = doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "Social", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create social activity: %d: %s", rec.Code, rec.Body.String())
	}
	var social activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &social); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec = doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/participants", social.ID), unfederatedToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected an unfederated member to still join a social activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateDefaultsToPublicVisibility(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Visibility != activities.VisibilityPublic {
		t.Errorf("expected visibility to default to public, got %q", created.Visibility)
	}
}

func TestHandlerPublicActivityJoinWorksAsBefore(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	memberToken := tokenFor(t, authn, member.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "visibility": "public", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)

	if rec := doRequest(mux, http.MethodPost, path, memberToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected a plain member to sign themselves up to a public activity, got %d: %s", rec.Code, rec.Body.String())
	}
	// The organizer alone (not admin/board) still can't add someone else
	// to a *public* activity — that extra privilege is private-only.
	if rec := doRequest(mux, http.MethodPost, path, leaderToken, map[string]any{"login": other.Login}); rec.Code != http.StatusForbidden {
		t.Errorf("expected the organizer to be forbidden from adding someone else to a public activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add someone else to a public activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerPrivateActivityOnlyOrganizerBoardOrAdminCanAddParticipants(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	admin := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	memberToken := tokenFor(t, authn, member.ID, "member")
	boardToken := tokenFor(t, authn, board.ID, "board")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "visibility": "private", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Visibility != activities.VisibilityPrivate {
		t.Fatalf("expected a private activity, got %q", created.Visibility)
	}
	path := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)

	// A plain, unrelated member can't even sign *themselves* up.
	if rec := doRequest(mux, http.MethodPost, path, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from self-joining a private activity, got %d", rec.Code)
	}
	if code := errorCode(t, doRequest(mux, http.MethodPost, path, memberToken, nil)); code != "forbidden" {
		t.Errorf("expected error code forbidden, got %q", code)
	}

	// The organizer can add that same member.
	if rec := doRequest(mux, http.MethodPost, path, leaderToken, map[string]any{"login": member.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected the organizer to add a member to their own private activity, got %d: %s", rec.Code, rec.Body.String())
	}
	// Board and admin can too.
	if rec := doRequest(mux, http.MethodPost, path, boardToken, map[string]any{"login": board.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected board to add a participant to a private activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]any{"login": admin.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add a participant to a private activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerLeaveSelfOrAdminBoardOnly(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	memberToken := tokenFor(t, authn, member.ID, "member")
	boardToken := tokenFor(t, authn, board.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "visibility": "public", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"participant_logins": []string{member.Login, other.Login},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pathFor := func(login string) string {
		return fmt.Sprintf("/api/v1/activities/%d/participants/%s", created.ID, login)
	}

	// A plain member can't remove someone else...
	if rec := doRequest(mux, http.MethodDelete, pathFor(other.Login), memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a plain member to be forbidden from removing someone else, got %d", rec.Code)
	}
	if code := errorCode(t, doRequest(mux, http.MethodDelete, pathFor(other.Login), memberToken, nil)); code != "forbidden" {
		t.Errorf("expected error code forbidden, got %q", code)
	}
	// ...but can remove themselves.
	if rec := doRequest(mux, http.MethodDelete, pathFor(member.Login), memberToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected a member to remove themselves, got %d: %s", rec.Code, rec.Body.String())
	}
	// Board can remove anyone.
	if rec := doRequest(mux, http.MethodDelete, pathFor(other.Login), boardToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected board to remove someone else, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerLeavePrivateActivityOrganizerCanRemoveAnyone(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "visibility": "private", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	participantsPath := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)
	if rec := doRequest(mux, http.MethodPost, participantsPath, leaderToken, map[string]any{"login": member.Login}); rec.Code != http.StatusNoContent {
		t.Fatalf("organizer adds member: %d: %s", rec.Code, rec.Body.String())
	}

	// The mirror image of adding: the organizer of a *private* activity
	// can remove anyone from it too, not just admin/board or the person
	// themselves.
	leavePath := fmt.Sprintf("/api/v1/activities/%d/participants/%s", created.ID, member.Login)
	if rec := doRequest(mux, http.MethodDelete, leavePath, leaderToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected the organizer to remove a participant from their own private activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerLeavePublicActivityOrganizerCannotRemoveSomeoneElse(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "visibility": "public", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	participantsPath := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)
	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("self join: %d: %s", rec.Code, rec.Body.String())
	}

	// Unlike a private activity, the organizer of a *public* one gets no
	// extra say over who's in it — same as they get none over who may
	// join it in the first place.
	leavePath := fmt.Sprintf("/api/v1/activities/%d/participants/%s", created.ID, member.Login)
	if rec := doRequest(mux, http.MethodDelete, leavePath, leaderToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected the organizer of a public activity to be forbidden from removing someone else, got %d", rec.Code)
	}
}

func TestHandlerLeaveActivityNotFound(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	member := testutil.InsertUser(t, tx, "")
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/participants/%s", 999999, member.Login), memberToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerLeaveNotifiesTelegramDifferentlyForSelfVersusOther(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	admin := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"participant_logins": []string{member.Login},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification

	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/participants/%s", created.ID, admin.Login), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin removes self (never actually a participant here, still allowed): %d: %s", rec.Code, rec.Body.String())
	}
	selfMsg := notifier.waitForMessage(t)
	if !strings.Contains(selfMsg, admin.Login) || !strings.Contains(selfMsg, "se ha dado de baja") {
		t.Errorf("expected a self-removal message naming %q, got %q", admin.Login, selfMsg)
	}

	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/participants/%s", created.ID, member.Login), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin removes other: %d: %s", rec.Code, rec.Body.String())
	}
	otherMsg := notifier.waitForMessage(t)
	if !strings.Contains(otherMsg, admin.Login) || !strings.Contains(otherMsg, member.Login) || !strings.Contains(otherMsg, "ha dado de baja a") {
		t.Errorf("expected a message naming both actor %q and target %q, got %q", admin.Login, member.Login, otherMsg)
	}
}

func TestHandlerUpdateCanChangeVisibility(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"visibility": "private"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update visibility: %d: %s", rec.Code, rec.Body.String())
	}
	var updated activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Visibility != activities.VisibilityPrivate {
		t.Errorf("expected visibility to change to private, got %q", updated.Visibility)
	}
}

// Board (and admin) can reassign an activity's organizer after the
// fact — the whole point being that leadership doesn't have to stay
// stuck with whoever happened to create it.
func TestHandlerUpdateAllowsBoardToChangeLeader(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	newLeader := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	boardToken := tokenFor(t, authn, board.ID, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	rec = doRequest(mux, http.MethodPatch, path, boardToken, map[string]any{"leader_login": newLeader.Login})
	if rec.Code != http.StatusOK {
		t.Fatalf("update leader: %d: %s", rec.Code, rec.Body.String())
	}
	var updated activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.LeaderLogin != newLeader.Login {
		t.Errorf("expected leader_login %q, got %q", newLeader.Login, updated.LeaderLogin)
	}

	// The old leader lost canManage the moment they stopped being the
	// leader — even though editing other fields is otherwise allowed
	// for "the organizer".
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"title": "New"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected the ex-organizer to lose manage access, got %d", rec.Code)
	}
}

// The organizer can edit their own activity's other fields, but can't
// hand it off to someone else themselves — only admin/board can.
func TestHandlerUpdateRejectsLeaderChangingLeader(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	rec = doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"leader_login": other.Login})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for the organizer reassigning their own activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Any baseline role can see the holidays list — it's what lets the
// calendar view mark them, not sensitive data.
func TestHandlerListHolidaysIsOpenToAnyBaselineUser(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/holidays?year=2026", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []activities.Holiday
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) == 0 {
		t.Error("expected the seeded 2026 holidays to be listed")
	}
}

func TestHandlerCreateHolidayRequiresAdminOrBoard(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardToken := tokenFor(t, authn, 2, "board")

	body := map[string]any{"date": "2026-04-06", "name": "Lunes de Pascua"}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/holidays", memberToken, body); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from creating a holiday, got %d", rec.Code)
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/holidays", boardToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected board to create a holiday, got %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Holiday
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Name != "Lunes de Pascua" {
		t.Errorf("expected name %q, got %q", "Lunes de Pascua", created.Name)
	}
}

func TestHandlerCreateHolidayRejectsDuplicateDate(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	// 2026-01-01 is already seeded (migration 00009_holidays.sql).
	rec := doRequest(mux, http.MethodPost, "/api/v1/holidays", boardToken, map[string]any{
		"date": "2026-01-01", "name": "Duplicado",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate holiday date, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "duplicate_holiday" {
		t.Errorf("expected error code duplicate_holiday, got %q", code)
	}
}

func TestHandlerDeleteHolidayRequiresAdminOrBoard(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardToken := tokenFor(t, authn, 2, "board")

	var holidayID int64
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO holidays (date, name) VALUES ('2026-04-06', 'Lunes de Pascua') RETURNING id
	`).Scan(&holidayID); err != nil {
		t.Fatalf("insert holiday: %v", err)
	}

	path := fmt.Sprintf("/api/v1/holidays/%d", holidayID)
	if rec := doRequest(mux, http.MethodDelete, path, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from deleting a holiday, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, path, boardToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("expected board to delete the holiday, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerDeleteHolidayNotFound(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/holidays/999999", boardToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an unknown holiday id, got %d", rec.Code)
	}
}

// The list defaults to newest-first (unchanged); ?order=asc switches
// to oldest-first, for the activities list page.
// TestHandlerListClampsNegativeOffset covers the fix for a previously
// unhandled 500: a negative offset used to reach Postgres's own OFFSET
// clause untouched, which rejects it outright. Now defaulted to the
// start instead, same as an out-of-range limit already was.
func TestHandlerListClampsNegativeOffset(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	member := testutil.InsertUser(t, tx, "")
	token := tokenFor(t, authn, member.ID, "member")
	if rec := doRequest(mux, http.MethodGet, "/api/v1/activities?offset=-1", token, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a negative offset to be clamped rather than error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListOrderDefaultsToDescendingAndCanBeAscending(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertUser(t, tx, "")
	token := tokenFor(t, authn, leader.ID, "member")

	older := doRequest(mux, http.MethodPost, "/api/v1/activities", token, map[string]any{
		"title": "Older", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if older.Code != http.StatusCreated {
		t.Fatalf("create older: %d: %s", older.Code, older.Body.String())
	}
	newer := doRequest(mux, http.MethodPost, "/api/v1/activities", token, map[string]any{
		"title": "Newer", "type": "social", "start_date": "2026-02-01", "end_date": "2026-02-01",
	})
	if newer.Code != http.StatusCreated {
		t.Fatalf("create newer: %d: %s", newer.Code, newer.Body.String())
	}
	var olderActivity, newerActivity activities.Activity
	if err := json.Unmarshal(older.Body.Bytes(), &olderActivity); err != nil {
		t.Fatalf("decode older: %v", err)
	}
	if err := json.Unmarshal(newer.Body.Bytes(), &newerActivity); err != nil {
		t.Fatalf("decode newer: %v", err)
	}

	rec := doRequest(mux, http.MethodGet, "/api/v1/activities", token, nil)
	var descending []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &descending); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if descending[0].ID != newerActivity.ID || descending[1].ID != olderActivity.ID {
		t.Errorf("expected newest first by default, got [%d, %d]", descending[0].ID, descending[1].ID)
	}

	rec = doRequest(mux, http.MethodGet, "/api/v1/activities?order=asc", token, nil)
	var ascending []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &ascending); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ascending[0].ID != olderActivity.ID || ascending[1].ID != newerActivity.ID {
		t.Errorf("expected oldest first with order=asc, got [%d, %d]", ascending[0].ID, ascending[1].ID)
	}
}

func TestHandlerCreateNotifiesTelegramWithActorAndTitle(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Sima del Viento", "type": "caving", "start_date": "2026-03-01", "end_date": "2026-03-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, leader.Login) || !strings.Contains(msg, "Sima del Viento") {
		t.Errorf("expected the notification to name the actor and title, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff is the core of the
// feature this whole notifier exists for: seeing exactly what changed,
// not just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification first

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"title": "B"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, leader.Login) {
		t.Errorf("expected the notification to name the actor, got %q", msg)
	}
	if !strings.Contains(msg, "Title: A → B") {
		t.Errorf("expected the notification to show the title change, got %q", msg)
	}
}

// TestHandlerUpdateWithNoActualChangeSendsNoNotification makes sure a
// PATCH that doesn't actually change anything doesn't produce a
// misleading "edited" message with an empty diff.
func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification first

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"title": "A"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

func TestHandlerCancelNotifiesTelegramWithActorAndTitle(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertUser(t, tx, "")
	leaderToken := tokenFor(t, authn, leader.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Cancelada", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification first

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	if rec := doRequest(mux, http.MethodDelete, path, leaderToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, leader.Login) || !strings.Contains(msg, "Cancelada") {
		t.Errorf("expected the notification to name the actor and title, got %q", msg)
	}
}

func TestHandlerJoinNotifiesTelegramDifferentlyForSelfVersusOther(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	admin := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification first
	participantsPath := fmt.Sprintf("/api/v1/activities/%d/participants", created.ID)

	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("self join: %d: %s", rec.Code, rec.Body.String())
	}
	selfMsg := notifier.waitForMessage(t)
	if !strings.Contains(selfMsg, member.Login) || !strings.Contains(selfMsg, "se ha apuntado") {
		t.Errorf("expected a self-signup message naming %q, got %q", member.Login, selfMsg)
	}

	if rec := doRequest(mux, http.MethodPost, participantsPath, adminToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Fatalf("admin adds other: %d: %s", rec.Code, rec.Body.String())
	}
	otherMsg := notifier.waitForMessage(t)
	if !strings.Contains(otherMsg, admin.Login) || !strings.Contains(otherMsg, other.Login) || !strings.Contains(otherMsg, "ha apuntado") {
		t.Errorf("expected a message naming both actor %q and target %q, got %q", admin.Login, other.Login, otherMsg)
	}
}

func TestHandlerCreateHolidayNotifiesTelegram(t *testing.T) {
	mux, authn, _, notifier := newTestServerWithNotifier(t)
	boardToken := tokenFor(t, authn, 1, "board")

	body := map[string]any{"date": "2026-04-06", "name": "Lunes de Pascua"}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/holidays", boardToken, body); rec.Code != http.StatusCreated {
		t.Fatalf("create holiday: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, "Lunes de Pascua") || !strings.Contains(msg, "2026-04-06") {
		t.Errorf("expected the notification to name the holiday and date, got %q", msg)
	}
}

func TestHandlerDeleteHolidayNotifiesTelegram(t *testing.T) {
	mux, authn, _, notifier := newTestServerWithNotifier(t)
	boardToken := tokenFor(t, authn, 1, "board")

	rec := doRequest(mux, http.MethodPost, "/api/v1/holidays", boardToken, map[string]any{
		"date": "2026-04-06", "name": "Lunes de Pascua",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create holiday: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Holiday
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	notifier.waitForMessage(t) // drain the create notification first

	rec = doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/holidays/%d", created.ID), boardToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete holiday: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, "Lunes de Pascua") {
		t.Errorf("expected the notification to name the deleted holiday, got %q", msg)
	}
}

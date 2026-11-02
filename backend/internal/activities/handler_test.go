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
	service := activities.NewService(activities.NewRepository(tx), licensesService, rolesService)
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
	service := activities.NewService(activities.NewRepository(tx), licensesService, rolesService)
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
	person := testutil.InsertMember(t, tx)
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
	person := testutil.InsertMember(t, tx)
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
	person := testutil.InsertMember(t, tx)
	memberToken := tokenFor(t, authn, person.ID, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/activities/999999", memberToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListActiveLeavesOutWhatIsOverOrCancelled(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	member := testutil.InsertMember(t, tx)
	admin := testutil.InsertMember(t, tx)
	memberToken := tokenFor(t, authn, member.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	soon := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "To come", "type": "social", "start_date": soon, "end_date": soon,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "Over", "type": "social", "start_date": "2020-01-01", "end_date": "2020-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create the old one: %d: %s", rec.Code, rec.Body.String())
	}

	listActive := func() []activities.Activity {
		t.Helper()
		rec := doRequest(mux, http.MethodGet, "/api/v1/activities?active=true", memberToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list active: %d: %s", rec.Code, rec.Body.String())
		}
		var list []activities.Activity
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return list
	}
	active := listActive()
	if len(active) != 1 || active[0].Title != "To come" || active[0].Cancelled {
		t.Fatalf("expected only the activity still to come, got %+v", active)
	}
	path := fmt.Sprintf("/api/v1/activities/%d", active[0].ID)

	// Cancelling it takes it out of the active ones, and cancelled=false brings it back.
	rec = doRequest(mux, http.MethodPatch, path, adminToken, map[string]any{"cancelled": true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel: %d: %s", rec.Code, rec.Body.String())
	}
	if active := listActive(); len(active) != 0 {
		t.Errorf("expected the cancelled activity to drop out of ?active=true, got %+v", active)
	}
	rec = doRequest(mux, http.MethodPatch, path, adminToken, map[string]any{"cancelled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate: %d: %s", rec.Code, rec.Body.String())
	}
	if active := listActive(); len(active) != 1 {
		t.Errorf("expected the reactivated activity back in ?active=true, got %+v", active)
	}

	// The unfiltered list has everything, whatever the dates and the cancellation.
	rec = doRequest(mux, http.MethodGet, "/api/v1/activities", memberToken, nil)
	var all []activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected the unfiltered list to include both, got %+v", all)
	}
}

func TestHandlerCreateWithParticipants(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertMember(t, tx)
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
	member := testutil.InsertMember(t, tx)
	admin := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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
	// In a public activity a member can sign up someone else too.
	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected a member to sign someone else up to a public activity (204), got %d: %s", rec.Code, rec.Body.String())
	}
	// admin/board can add anyone.
	if rec := doRequest(mux, http.MethodPost, participantsPath, adminToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add someone else (204), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserIsOpenToAnyBaselineUser(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertMember(t, tx)
	person := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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
	person := testutil.InsertMember(t, tx)
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
	board := testutil.InsertMember(t, tx)
	organizer := testutil.InsertMember(t, tx)
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
	person := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	otherMember := testutil.InsertMember(t, tx)
	admin := testutil.InsertMember(t, tx)
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
	cancel := map[string]any{"cancelled": true}
	if rec := doRequest(mux, http.MethodPatch, path, otherMemberToken, cancel); rec.Code != http.StatusForbidden {
		t.Errorf("expected a non-leader member to be forbidden from cancelling an activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, cancel); rec.Code != http.StatusOK {
		t.Errorf("expected the organizer to cancel their own activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateCanChangeType(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 invalid_date_range for an end_date before start_date, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Any member can join any activity, whatever its type and whether or not they are federated.
func TestHandlerAnyMemberJoinsAnyActivityFederatedOrNot(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx) // with no federative license
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	memberToken := tokenFor(t, authn, member.ID, "member")

	for _, kind := range []string{"caving", "canyoning", "social", "training", "other"} {
		rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
			"title": kind, "type": kind, "start_date": "2026-01-01", "end_date": "2026-01-01",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s activity: %d: %s", kind, rec.Code, rec.Body.String())
		}
		var a activities.Activity
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatalf("decode: %v", err)
		}
		rec = doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/participants", a.ID), memberToken, nil)
		if rec.Code != http.StatusNoContent {
			t.Errorf("expected an unfederated member to join a %s activity, got %d: %s", kind, rec.Code, rec.Body.String())
		}
	}
}

// Only a member (a socio now) can be signed up: not somebody who never was, nor a former member, whoever
// does it and however it is done.
func TestHandlerOnlyAMemberCanBeSignedUp(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertMember(t, tx)
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	neverMember := testutil.InsertUser(t, tx, "")
	former := testutil.InsertUser(t, tx, "")
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO memberships (user_id, start_date, end_date) VALUES ($1, CURRENT_DATE - 400, CURRENT_DATE - 30)`, former.ID); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "Salida", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	var a activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := fmt.Sprintf("/api/v1/activities/%d/participants", a.ID)

	for name, person := range map[string]testutil.TestUser{"somebody who never was a member": neverMember, "a former member": former} {
		// Signing up themselves, even with a token that says they are members.
		rec := doRequest(mux, http.MethodPost, path, tokenFor(t, authn, person.ID, "member"), nil)
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_a_member" {
			t.Errorf("%s joining: %d %s", name, rec.Code, rec.Body.String())
		}
		// Admin adding them.
		rec = doRequest(mux, http.MethodPost, path, adminToken, map[string]any{"login": person.Login})
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_a_member" {
			t.Errorf("admin adding %s: %d %s", name, rec.Code, rec.Body.String())
		}
		// At creation.
		rec = doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
			"title": "Otra", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
			"participant_logins": []string{person.Login},
		})
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "not_a_member" {
			t.Errorf("creating with %s as a participant: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

// The list of participants says, besides who they are, whether each has a federative license this year.
func TestHandlerParticipantsSayWhetherTheyAreFederated(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	admin := testutil.InsertMember(t, tx)
	federated := testutil.InsertMember(t, tx)
	plain := testutil.InsertMember(t, tx)
	adminToken := tokenFor(t, authn, admin.ID, "admin")
	var licenseType int64
	if err := tx.QueryRow(context.Background(), `SELECT id FROM license_types ORDER BY id LIMIT 1`).Scan(&licenseType); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(),
		`INSERT INTO licenses (user_id, license_type_id, issued_date, year) VALUES ($1, $2, CURRENT_DATE, EXTRACT(year FROM CURRENT_DATE))`, federated.ID, licenseType); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "Sima", "type": "caving", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"participant_logins": []string{federated.Login, plain.Login},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var a activities.Activity
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/participants", a.ID), adminToken, nil)
	var list []activities.Participant
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("participants: %v %s", err, rec.Body.String())
	}
	for _, p := range list {
		if want := p.UserLogin == federated.Login; p.Federated != want {
			t.Errorf("%s: federated = %v, want %v", p.UserLogin, p.Federated, want)
		}
	}
}

func TestHandlerCreateDefaultsToPublicVisibility(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
	admin := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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
	// Anybody can sign up anybody to a public activity, the organizer and a plain member included.
	if rec := doRequest(mux, http.MethodPost, path, leaderToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected the organizer to sign someone else up to a public activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, path, adminToken, map[string]any{"login": other.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add someone else to a public activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerPrivateActivityOnlyOrganizerBoardAdminOrParticipantsCanAddParticipants(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
	board := testutil.InsertMember(t, tx)
	admin := testutil.InsertMember(t, tx)
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
	// Now that the member is signed up, they can sign somebody else up — but somebody who is not
	// signed up still cannot, whoever they name.
	stranger := testutil.InsertMember(t, tx)
	third := testutil.InsertMember(t, tx)
	strangerToken := tokenFor(t, authn, stranger.ID, "member")
	if rec := doRequest(mux, http.MethodPost, path, strangerToken, map[string]any{"login": third.Login}); rec.Code != http.StatusForbidden {
		t.Errorf("expected somebody not signed up to be forbidden from adding to a private activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, path, memberToken, map[string]any{"login": third.Login}); rec.Code != http.StatusNoContent {
		t.Errorf("expected a participant to add somebody to a private activity, got %d: %s", rec.Code, rec.Body.String())
	}
	// Nor can somebody not signed up sign themselves up.
	if rec := doRequest(mux, http.MethodPost, path, strangerToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected somebody not signed up to be forbidden from joining a private activity, got %d", rec.Code)
	}
}

// Who signed each person up, and when, is kept and listed: themselves, somebody else, or whoever
// created the activity with them.
func TestHandlerParticipantsRecordWhoSignedThemUpAndWhen(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	creator := testutil.InsertMember(t, tx)
	atCreation := testutil.InsertMember(t, tx)
	byThemselves := testutil.InsertMember(t, tx)
	byAnother := testutil.InsertMember(t, tx)
	creatorToken := tokenFor(t, authn, creator.ID, "member")
	selfToken := tokenFor(t, authn, byThemselves.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", creatorToken, map[string]any{
		"title": "A", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
		"participant_logins": []string{atCreation.Login},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var a activities.Activity
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	path := fmt.Sprintf("/api/v1/activities/%d/participants", a.ID)
	if rec := doRequest(mux, http.MethodPost, path, selfToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("self sign-up: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPost, path, selfToken, map[string]any{"login": byAnother.Login}); rec.Code != http.StatusNoContent {
		t.Fatalf("sign-up by another: %d", rec.Code)
	}
	// Signing somebody up again changes nothing: the first record stays.
	if rec := doRequest(mux, http.MethodPost, path, creatorToken, map[string]any{"login": byAnother.Login}); rec.Code != http.StatusNoContent {
		t.Fatalf("second sign-up: %d", rec.Code)
	}

	before := time.Now().Add(-time.Minute)
	rec = doRequest(mux, http.MethodGet, path, creatorToken, nil)
	var list []activities.Participant
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 3 {
		t.Fatalf("participants: %v %s", err, rec.Body.String())
	}
	want := map[string]string{atCreation.Login: creator.Login, byThemselves.Login: byThemselves.Login, byAnother.Login: byThemselves.Login}
	for _, p := range list {
		if p.AddedByLogin != want[p.UserLogin] {
			t.Errorf("%s was signed up by %q, want %s", p.UserLogin, p.AddedByLogin, want[p.UserLogin])
		}
		if p.AddedAt.Before(before) || p.AddedAt.After(time.Now().Add(time.Minute)) {
			t.Errorf("%s: added_at %v is not about now", p.UserLogin, p.AddedAt)
		}
	}
}

func TestHandlerLeaveSelfOrAdminBoardOnly(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
	board := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
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
	member := testutil.InsertMember(t, tx)
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/participants/%s", 999999, member.Login), memberToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent activity, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerLeaveNotifiesTelegramDifferentlyForSelfVersusOther(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	admin := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	newLeader := testutil.InsertMember(t, tx)
	board := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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
	member := testutil.InsertMember(t, tx)
	token := tokenFor(t, authn, member.ID, "member")
	if rec := doRequest(mux, http.MethodGet, "/api/v1/activities?offset=-1", token, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a negative offset to be clamped rather than error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListOrderDefaultsToDescendingAndCanBeAscending(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	leader := testutil.InsertMember(t, tx)
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
	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"cancelled": true}); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, leader.Login) || !strings.Contains(msg, "Cancelada") || !strings.Contains(msg, "ha cancelado") {
		t.Errorf("expected the notification to say who cancelled which activity, got %q", msg)
	}

	if rec := doRequest(mux, http.MethodPatch, path, leaderToken, map[string]any{"cancelled": false}); rec.Code != http.StatusOK {
		t.Fatalf("reactivate: %d: %s", rec.Code, rec.Body.String())
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, "ha reactivado") {
		t.Errorf("expected the notification to say it was reactivated, got %q", msg)
	}
}

func TestHandlerJoinNotifiesTelegramDifferentlyForSelfVersusOther(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	admin := testutil.InsertMember(t, tx)
	member := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
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

// A bad type, visibility or date range is the caller's mistake: a
// 400 with its own code, not the 500 the database's enum/CHECK errors used
// to become.
func TestHandlerRejectsInvalidActivityFields(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	person := testutil.InsertMember(t, tx)
	token := tokenFor(t, authn, person.ID, "member")
	valid := func() map[string]any {
		return map[string]any{"title": "A", "type": "social", "start_date": "2026-03-10", "end_date": "2026-03-12"}
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{"unknown type", func(b map[string]any) { b["type"] = "escalada" }, "invalid_type"},
		{"unknown visibility", func(b map[string]any) { b["visibility"] = "secret" }, "invalid_visibility"},
		{"end before start", func(b map[string]any) { b["end_date"] = "2026-03-09" }, "invalid_date_range"},
	}
	for _, c := range cases {
		body := valid()
		c.mutate(body)
		rec := doRequest(mux, http.MethodPost, "/api/v1/activities", token, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create, %s: expected 400, got %d: %s", c.name, rec.Code, rec.Body.String())
			continue
		}
		if got := errorCode(t, rec); got != c.code {
			t.Errorf("create, %s: expected error code %s, got %s", c.name, c.code, got)
		}
	}

	created := doRequest(mux, http.MethodPost, "/api/v1/activities", token, valid())
	if created.Code != http.StatusCreated {
		t.Fatalf("expected the valid activity to be created, got %d: %s", created.Code, created.Body.String())
	}
	var a activities.Activity
	if err := json.Unmarshal(created.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode: %v", err)
	}
	path := fmt.Sprintf("/api/v1/activities/%d", a.ID)
	updates := []struct {
		name string
		body map[string]any
		code string
	}{
		{"unknown type", map[string]any{"type": "escalada"}, "invalid_type"},
		{"unknown visibility", map[string]any{"visibility": "secret"}, "invalid_visibility"},
		// Either date can be sent alone: it's compared with the stored other one.
		{"end before the stored start", map[string]any{"end_date": "2026-03-09"}, "invalid_date_range"},
		{"start after the stored end", map[string]any{"start_date": "2026-03-13"}, "invalid_date_range"},
	}
	for _, u := range updates {
		rec := doRequest(mux, http.MethodPatch, path, token, u.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("update, %s: expected 400, got %d: %s", u.name, rec.Code, rec.Body.String())
			continue
		}
		if got := errorCode(t, rec); got != u.code {
			t.Errorf("update, %s: expected error code %s, got %s", u.name, u.code, got)
		}
	}
	if rec := doRequest(mux, http.MethodPatch, path, token, map[string]any{"start_date": "2026-03-11"}); rec.Code != http.StatusOK {
		t.Errorf("expected a start date still before the end date to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Whoever signed somebody up can take them off again, in a public or a private activity, even without
// being the organizer, admin or board; somebody else who is not any of those still cannot.
func TestHandlerWhoSignedSomebodyUpCanRemoveThem(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	leader := testutil.InsertMember(t, tx)
	signer := testutil.InsertMember(t, tx)
	signed := testutil.InsertMember(t, tx)
	bystander := testutil.InsertMember(t, tx)
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	signerToken := tokenFor(t, authn, signer.ID, "member")
	bystanderToken := tokenFor(t, authn, bystander.ID, "member")

	for _, visibility := range []string{"public", "private"} {
		rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
			"title": visibility, "type": "social", "visibility": visibility, "start_date": "2026-01-01", "end_date": "2026-01-01",
			"participant_logins": []string{signer.Login, bystander.Login},
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d: %s", visibility, rec.Code, rec.Body.String())
		}
		var a activities.Activity
		_ = json.Unmarshal(rec.Body.Bytes(), &a)
		base := fmt.Sprintf("/api/v1/activities/%d/participants", a.ID)
		path := base + "/" + signed.Login
		// The signer (already in, so allowed to add in a private one) signs somebody up.
		if rec := doRequest(mux, http.MethodPost, base, signerToken, map[string]any{"login": signed.Login}); rec.Code != http.StatusNoContent {
			t.Fatalf("%s: signing up: %d: %s", visibility, rec.Code, rec.Body.String())
		}
		// Another participant, who did not sign them up, cannot take them off (nor is the organizer of a public one).
		if rec := doRequest(mux, http.MethodDelete, path, bystanderToken, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s: a bystander removing somebody: %d", visibility, rec.Code)
		}
		// Whoever signed them up can.
		if rec := doRequest(mux, http.MethodDelete, path, signerToken, nil); rec.Code != http.StatusNoContent {
			t.Errorf("%s: the one who signed them up removing them: %d: %s", visibility, rec.Code, rec.Body.String())
		}
		// They are really gone, and the signer cannot remove somebody they did not sign up.
		if in, _ := doRequestIsParticipant(t, mux, base, leaderToken, signed.Login); in {
			t.Errorf("%s: still signed up after being removed", visibility)
		}
		if rec := doRequest(mux, http.MethodDelete, base+"/"+bystander.Login, signerToken, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s: removing somebody the signer did not sign up: %d", visibility, rec.Code)
		}
	}
}

func doRequestIsParticipant(t *testing.T, mux *http.ServeMux, base, token, login string) (bool, error) {
	t.Helper()
	rec := doRequest(mux, http.MethodGet, base, token, nil)
	var list []activities.Participant
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		return false, err
	}
	for _, p := range list {
		if p.UserLogin == login {
			return true, nil
		}
	}
	return false, nil
}

// DELETE deletes an activity for real, for its organizer, admin or board, while nothing is attached.
func TestHandlerDeleteActivity(t *testing.T) {
	mux, authn, tx, notifier := newTestServerWithNotifier(t)
	leader := testutil.InsertMember(t, tx)
	other := testutil.InsertMember(t, tx)
	leaderToken := tokenFor(t, authn, leader.ID, "member")
	otherToken := tokenFor(t, authn, other.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", leaderToken, map[string]any{
		"title": "Por error", "type": "social", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	notifier.waitForMessage(t) // the creation's
	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)

	if rec := doRequest(mux, http.MethodDelete, path, otherToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member who is not the organizer to be forbidden, got %d", rec.Code)
	}
	// Somebody signed up: refused until they are taken off.
	if rec := doRequest(mux, http.MethodPost, path+"/participants", otherToken, nil); rec.Code != http.StatusNoContent && rec.Code != http.StatusCreated {
		t.Fatalf("sign up: %d: %s", rec.Code, rec.Body.String())
	}
	notifier.waitForMessage(t)
	rec = doRequest(mux, http.MethodDelete, path, leaderToken, nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "activity_in_use" {
		t.Errorf("an activity with a participant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("%s/participants/%s", path, other.Login), otherToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("leave: %d: %s", rec.Code, rec.Body.String())
	}
	notifier.waitForMessage(t)

	if rec := doRequest(mux, http.MethodDelete, path, leaderToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d: %s", rec.Code, rec.Body.String())
	}
	if msg := notifier.waitForMessage(t); !strings.Contains(msg, "ha borrado") || !strings.Contains(msg, "Por error") {
		t.Errorf("expected the notice to say who deleted which activity, got %q", msg)
	}
	if rec := doRequest(mux, http.MethodGet, path, leaderToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("expected it to be gone, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, "/api/v1/activities/abc", leaderToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", rec.Code)
	}
}

// Guests are people from outside the club, invited by somebody signed up to the activity. That
// person adds them (with just a name); the organizer, admin and board say who of those signed up
// invites them. The same people remove them.
func TestHandlerGuests(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	organizer := testutil.InsertMember(t, tx)
	host := testutil.InsertMember(t, tx)
	outsider := testutil.InsertMember(t, tx) // a member, but not signed up
	admin := testutil.InsertMember(t, tx)
	organizerToken := tokenFor(t, authn, organizer.ID, "member")
	hostToken := tokenFor(t, authn, host.ID, "member")
	outsiderToken := tokenFor(t, authn, outsider.ID, "member")
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", organizerToken, map[string]any{
		"title": "Con invitados", "type": "caving", "start_date": "2026-01-01", "end_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	base := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	guests := base + "/guests"
	if rec := doRequest(mux, http.MethodPost, base+"/participants", hostToken, nil); rec.Code != http.StatusNoContent && rec.Code != http.StatusCreated {
		t.Fatalf("sign up: %d: %s", rec.Code, rec.Body.String())
	}
	add := func(token string, body map[string]any) *httptest.ResponseRecorder {
		return doRequest(mux, http.MethodPost, guests, token, body)
	}
	decodeGuest := func(rec *httptest.ResponseRecorder) activities.Guest {
		t.Helper()
		var g activities.Guest
		if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return g
	}

	// Whoever invites, being signed up, gives just the name.
	rec = add(hostToken, map[string]any{"name": "  Marta Pérez  "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("a participant adds a guest: %d %s", rec.Code, rec.Body.String())
	}
	own := decodeGuest(rec)
	if own.Name != "Marta Pérez" || own.InvitedByLogin != host.Login || own.AddedByLogin != host.Login {
		t.Errorf("unexpected guest: %+v", own)
	}
	// Naming themselves is fine, naming somebody else is not.
	if rec := add(hostToken, map[string]any{"name": "Otro", "invited_by": host.Login}); rec.Code != http.StatusCreated {
		t.Errorf("a participant naming themselves: %d %s", rec.Code, rec.Body.String())
	}
	if rec := add(hostToken, map[string]any{"name": "Otro más", "invited_by": organizer.Login}); rec.Code != http.StatusForbidden {
		t.Errorf("a participant naming somebody else: %d %s", rec.Code, rec.Body.String())
	}
	// Somebody not signed up cannot add one.
	if rec := add(outsiderToken, map[string]any{"name": "Pedro"}); rec.Code != http.StatusForbidden {
		t.Errorf("somebody not signed up: %d %s", rec.Code, rec.Body.String())
	}

	// The organizer and admin have to say who invites them, who has to be signed up.
	for name, token := range map[string]string{"the organizer": organizerToken, "admin": adminToken} {
		rec := add(token, map[string]any{"name": "Luis"})
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invited_by_required" {
			t.Errorf("%s without invited_by: %d %s", name, rec.Code, rec.Body.String())
		}
		rec = add(token, map[string]any{"name": "Luis", "invited_by": outsider.Login})
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "inviter_not_signed_up" {
			t.Errorf("%s naming somebody not signed up: %d %s", name, rec.Code, rec.Body.String())
		}
		if rec := add(token, map[string]any{"name": "Luis", "invited_by": "nadie.nadie"}); rec.Code != http.StatusNotFound {
			t.Errorf("%s naming nobody: %d", name, rec.Code)
		}
	}
	rec = add(organizerToken, map[string]any{"name": "Elena", "invited_by": host.Login})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the organizer adds one for the host: %d %s", rec.Code, rec.Body.String())
	}
	byOrganizer := decodeGuest(rec)
	if byOrganizer.InvitedByLogin != host.Login || byOrganizer.AddedByLogin != organizer.Login {
		t.Errorf("expected invited by the host, added by the organizer: %+v", byOrganizer)
	}
	rec = add(adminToken, map[string]any{"name": "Raúl", "invited_by": host.Login})
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin adds one: %d %s", rec.Code, rec.Body.String())
	}
	byAdmin := decodeGuest(rec)

	// The same name from the same person is a duplicate, whatever its case; a blank one is not a name.
	if rec := add(hostToken, map[string]any{"name": "marta pérez"}); rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate_guest" {
		t.Errorf("a duplicate: %d %s", rec.Code, rec.Body.String())
	}
	if rec := add(hostToken, map[string]any{"name": "   "}); rec.Code != http.StatusBadRequest {
		t.Errorf("a blank name: %d", rec.Code)
	}

	// Anybody signed in lists them.
	rec = doRequest(mux, http.MethodGet, guests, outsiderToken, nil)
	var list []activities.Guest
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list) != 4 {
		t.Errorf("listing: %d, %d guests: %s", rec.Code, len(list), rec.Body.String())
	}

	// The host cannot leave with guests; they have to be removed first.
	leave := fmt.Sprintf("%s/participants/%s", base, host.Login)
	if rec := doRequest(mux, http.MethodDelete, leave, hostToken, nil); rec.Code != http.StatusConflict || errorCode(t, rec) != "has_guests" {
		t.Errorf("leaving with guests: %d %s", rec.Code, rec.Body.String())
	}

	// Removing: whoever invited them, the organizer, admin and board; nobody else.
	one := func(g activities.Guest) string { return fmt.Sprintf("%s/%d", guests, g.ID) }
	if rec := doRequest(mux, http.MethodDelete, one(own), outsiderToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("somebody else removing a guest: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, one(own), hostToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("the host removing their own: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, one(byOrganizer), organizerToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("the organizer removing one: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, one(byAdmin), adminToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("admin removing one: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, one(byAdmin), adminToken, nil); rec.Code != http.StatusNotFound {
		t.Errorf("removing it twice: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, guests+"/abc", adminToken, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad id: %d", rec.Code)
	}
	// One guest left ("Otro", the host's): when it goes the host can leave.
	rec = doRequest(mux, http.MethodGet, guests, hostToken, nil)
	list = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected one guest left, got %+v", list)
	}
	if rec := doRequest(mux, http.MethodDelete, one(list[0]), hostToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("removing the last: %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, leave, hostToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("leaving with no guests: %d %s", rec.Code, rec.Body.String())
	}
}

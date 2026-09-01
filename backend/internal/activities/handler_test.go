package activities_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

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
	activities.NewHandler(service, authenticator, usersService).RegisterRoutes(mux)
	return mux, resolver, tx
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

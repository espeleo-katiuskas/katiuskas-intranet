package activities_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/activities"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/testutil"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver) {
	mux, authenticator, _ := newTestServerWithTx(t)
	return mux, authenticator
}

// newTestServerWithTx also returns the underlying transaction, for tests
// that need to insert real user rows to satisfy a foreign key (e.g.
// signing someone up as a participant).
func newTestServerWithTx(t *testing.T) (*http.ServeMux, testutil.FakeResolver, pgx.Tx) {
	tx := testutil.NewTx(t)
	service := activities.NewService(activities.NewRepository(tx))
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	activities.NewHandler(service, authenticator).RegisterRoutes(mux)
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
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	body := map[string]any{
		"title": "Sima del Viento", "type": "caving", "location": "Cazorla", "start_date": "2026-09-15",
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
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "caving", "location": "X", "start_date": "2026-01-01",
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
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/activities/999999", memberToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandlerListActiveFiltersOutCompletedActivities(t *testing.T) {
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "Planned", "type": "caving", "location": "X", "start_date": "2026-01-01",
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

	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d", active[0].ID), tokenFor(t, authn, 2, "admin"), map[string]any{"status": "completed"})
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
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")

	body := map[string]any{
		"title": "Sima del Viento", "type": "caving", "location": "Cazorla", "start_date": "2026-09-15",
		"participant_user_ids": []int64{},
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with an empty participant list, got %d: %s", rec.Code, rec.Body.String())
	}

	body["participant_user_ids"] = []int64{999999}
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
		"title": "A", "type": "caving", "location": "X", "start_date": "2026-01-01",
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
	if rec := doRequest(mux, http.MethodPost, participantsPath, memberToken, map[string]any{"user_id": other.ID}); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member adding someone else to be forbidden, got %d", rec.Code)
	}
	// admin/board can add anyone.
	if rec := doRequest(mux, http.MethodPost, participantsPath, adminToken, map[string]any{"user_id": other.ID}); rec.Code != http.StatusNoContent {
		t.Errorf("expected admin to add someone else (204), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerListByUserIsOpenToAnyBaselineUser(t *testing.T) {
	mux, authn, tx := newTestServerWithTx(t)
	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	adminToken := tokenFor(t, authn, 1, "admin")
	memberToken := tokenFor(t, authn, other.ID, "member")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", adminToken, map[string]any{
		"title": "A", "type": "caving", "location": "X", "start_date": "2026-01-01",
		"participant_user_ids": []int64{person.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}

	path := fmt.Sprintf("/api/v1/users/%d/activities", person.ID)
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

func TestHandlerUpdateAndCancelStillRequireAdminOrBoard(t *testing.T) {
	mux, authn := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	adminToken := tokenFor(t, authn, 2, "admin")

	rec := doRequest(mux, http.MethodPost, "/api/v1/activities", memberToken, map[string]any{
		"title": "A", "type": "caving", "location": "X", "start_date": "2026-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	var created activities.Activity
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := fmt.Sprintf("/api/v1/activities/%d", created.ID)
	if rec := doRequest(mux, http.MethodPatch, path, memberToken, map[string]any{"title": "New"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from updating an activity, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodPatch, path, adminToken, map[string]any{"title": "New"}); rec.Code != http.StatusOK {
		t.Errorf("expected an admin to update an activity, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodDelete, path, memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member to be forbidden from cancelling an activity, got %d", rec.Code)
	}
}

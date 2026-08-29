package activities_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"katiuskas-intranet/backend/internal/activities"
	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/testutil"
)

func newTestServer(t *testing.T) (*http.ServeMux, *auth.Authenticator) {
	tx := testutil.NewTx(t)
	service := activities.NewService(activities.NewRepository(tx))
	authenticator := auth.NewAuthenticator("test-secret", time.Hour)

	mux := http.NewServeMux()
	activities.NewHandler(service, authenticator).RegisterRoutes(mux)
	return mux, authenticator
}

func tokenFor(t *testing.T, a *auth.Authenticator, id int64, roleNames ...string) string {
	t.Helper()
	token, err := a.IssueAccessToken(id, roleNames)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
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

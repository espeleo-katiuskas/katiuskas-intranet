package users_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *users.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	users.NewHandler(usersService, authenticator).RegisterRoutes(mux)
	return mux, resolver, usersService
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

func TestHandlerGetSelfVersusOthers(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{FirstName: "M", LastName: "Ember", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	other, err := svc.Create(ctx, users.CreateInput{FirstName: "O", LastName: "Ther", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s", member.Login), memberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected a member to view their own profile (200), got %d: %s", rec.Code, rec.Body.String())
	}

	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s", other.Login), memberToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected a member viewing someone else's profile to get 403, got %d", rec.Code)
	}

	adminToken := tokenFor(t, authn, 999999, "admin")
	if rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/users/%s", other.Login), adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected an admin to view anyone's profile (200), got %d", rec.Code)
	}
}

func TestHandlerListRequiresABaselineRole(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	memberToken := tokenFor(t, authn, 1, "member")
	boardToken := tokenFor(t, authn, 2, "board")
	adminToken := tokenFor(t, authn, 3, "admin")
	materialToken := tokenFor(t, authn, 4, "material") // not a baseline role

	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a member listing users, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", boardToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for board listing users, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", adminToken, nil); rec.Code != http.StatusOK {
		t.Errorf("expected 200 for admin listing users, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", materialToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for someone with no baseline role, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodGet, "/api/v1/users", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

// A plain member sees everyone on the list (not just themselves), but
// only MemberListing's fields — board and admin still get the full
// picture.
func TestHandlerListRestrictsFieldsForAPlainMemberButNotBoardOrAdmin(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	nationalID := "12345678A"
	birthDate, err := time.Parse("2006-01-02", "2000-01-01")
	if err != nil {
		t.Fatalf("parse birth date: %v", err)
	}
	if _, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Target", LastName: "Person", Email: strPtr(testutil.UniqueEmail(t)), NationalID: &nationalID, BirthDate: birthDate,
	}); err != nil {
		t.Fatalf("create target: %v", err)
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("national_id")) {
		t.Errorf("expected a plain member's listing to hide national_id entirely, got %s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("birth_date")) {
		t.Errorf("expected a plain member's listing to hide the exact birth_date, got %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"age_category":"adult"`)) {
		t.Errorf("expected the coarse age_category to still be visible to a member, got %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"first_name":"Target"`)) {
		t.Errorf("expected first_name to still be visible to a member, got %s", rec.Body.String())
	}

	boardToken := tokenFor(t, authn, 2, "board")
	rec = doRequest(mux, http.MethodGet, "/api/v1/users", boardToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(nationalID)) {
		t.Errorf("expected board to still see national_id, got %s", rec.Body.String())
	}
}

// Creating and deleting a person are admin-only now — board lost every
// way to modify a person's data, this included.
func TestHandlerCreateAndDeleteRequireAdminNotBoard(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()
	boardToken := tokenFor(t, authn, 1, "board")

	createBody := map[string]any{"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t)}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/users", boardToken, createBody); rec.Code != http.StatusForbidden {
		t.Errorf("expected board creating a person to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}

	target, err := svc.Create(ctx, users.CreateInput{FirstName: "T", LastName: "Arget", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s", target.Login), boardToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("expected board deleting a person to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

// There's no "grant access" step anymore — creating a person always
// succeeds regardless of whether a phone is given. Without one, they
// simply can't ever be found by FindActiveByEmailAndPhone (see the
// users package doc comment), which isn't something creation needs to
// validate up front.
func TestHandlerCreateWithoutPhoneSucceeds(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t), "birth_date": "2000-01-01",
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Symmetric to TestHandlerCreateWithoutPhoneSucceeds: email is optional
// too now — someone with neither on file just can never be matched by
// FindActiveByEmailAndPhone, so they can never sign in, but they can
// still exist in the club's records.
func TestHandlerCreateWithoutEmailSucceeds(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "birth_date": "2000-01-01",
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Unlike email and phone, birth_date is required — every person on file
// needs one now, not just to compute age_category.
func TestHandlerCreateRequiresBirthDate(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t),
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing birth_date, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_date" {
		t.Errorf("expected error code invalid_date, got %q", code)
	}
}

func TestHandlerCreateRejectsUnknownRole(t *testing.T) {
	mux, authn, _ := newTestServer(t)
	adminToken := tokenFor(t, authn, 1, "admin")

	body := map[string]any{
		"first_name": "A", "last_name": "B", "email": testutil.UniqueEmail(t), "birth_date": "2000-01-01",
		"role_names": []string{"not-a-real-role"},
	}
	rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_role" {
		t.Errorf("expected error code unknown_role, got %q", code)
	}
}

func TestHandlerDeleteCannotTargetSelf(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Self", LastName: "Admin", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	rec := doRequest(mux, http.MethodDelete, "/api/v1/users/"+admin.Login, adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "cannot_delete_self" {
		t.Errorf("expected error code cannot_delete_self, got %q", code)
	}
}

func TestHandlerDeleteBlocksTheLastAdminEndToEnd(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	solo, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Solo", LastName: "Admin", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// A different caller (not the admin under deletion) tries to delete them.
	callerToken := tokenFor(t, authn, 999999, "admin")

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/users/%s", solo.Login), callerToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "last_admin" {
		t.Errorf("expected error code last_admin, got %q", code)
	}
}

func TestHandlerDirectoryIsOpenToAnyAuthenticatedUserButHidesPersonalData(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	phone := "600111222"
	person, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Directory", LastName: "Entry", Email: strPtr(testutil.UniqueEmail(t)),
		Phone: &phone,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	memberToken := tokenFor(t, authn, 999999, "member")

	rec := doRequest(mux, http.MethodGet, "/api/v1/users/directory", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a plain member to read the directory (200), got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte(fmt.Sprintf(`"login":%q`, person.Login))) {
		t.Errorf("expected the directory to include the created person, got %s", body)
	}
	var entries []users.DirectoryEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode directory: %v", err)
	}
	if body := rec.Body.String(); bytes.Contains([]byte(body), []byte(phone)) {
		t.Errorf("expected the directory to never expose phone numbers, got %s", body)
	}

	if rec := doRequest(mux, http.MethodGet, "/api/v1/users/directory", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

func TestHandlerUpdateSelfVersusOthers(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{FirstName: "M", LastName: "Ember", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	other, err := svc.Create(ctx, users.CreateInput{FirstName: "O", LastName: "Ther", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", member.Login), memberToken, map[string]any{"city": "Madrid"})
	if rec.Code != http.StatusOK {
		t.Errorf("expected a member to edit their own profile (200), got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", other.Login), memberToken, map[string]any{"city": "Madrid"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected a member editing someone else's profile to get 403, got %d", rec.Code)
	}

	// Board lost the general ability to edit anyone's profile along with
	// every other non-admin caller — it's not just members who are
	// limited to their own record now.
	boardToken := tokenFor(t, authn, 999998, "board")
	rec = doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", other.Login), boardToken, map[string]any{"city": "Madrid"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected board editing someone else's profile to be forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Self-service edits — the only kind left for anyone but admin — are
// limited to contact fields: name, national ID, birth date, and gender
// stay off-limits even on your own record.
func TestHandlerUpdateRestrictsFieldsForNonAdminSelfEdit(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{FirstName: "M", LastName: "Ember", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	allowed := map[string]any{
		"email": testutil.UniqueEmail(t), "phone": "611222333", "address": "Calle Mayor 1",
		"postal_code": "28080", "city": "Madrid", "province": "Madrid", "emergency_contact": "Alguien, 611000000",
	}
	if rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", member.Login), memberToken, allowed); rec.Code != http.StatusOK {
		t.Errorf("expected all-contact-fields self-edit to succeed, got %d: %s", rec.Code, rec.Body.String())
	}

	restrictedBodies := []map[string]any{
		{"first_name": "New"},
		{"last_name": "New"},
		{"national_id": "12345678A"},
		{"birth_date": "2000-01-01"},
		{"gender": "male"},
	}
	for _, body := range restrictedBodies {
		rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", member.Login), memberToken, body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected self-editing %v to be forbidden, got %d: %s", body, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "restricted_fields" {
			t.Errorf("expected error code restricted_fields for %v, got %q", body, code)
		}
	}

	adminToken := tokenFor(t, authn, 999997, "admin")
	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", member.Login), adminToken, map[string]any{"first_name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Errorf("expected admin to still edit any field on anyone, got %d: %s", rec.Code, rec.Body.String())
	}
}

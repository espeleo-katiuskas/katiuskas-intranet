package users_test

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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *users.Service) {
	mux, resolver, usersService, _ := newTestServerWithMemberships(t)
	return mux, resolver, usersService
}

// newTestServerWithMemberships is newTestServer, but also returns the
// memberships.Service backing it — for the few tests that need to give
// someone an actual alta, since GET /users only shows a plain member
// people who are or have been a socio (see Handler.list).
func newTestServerWithMemberships(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *users.Service, *memberships.Service) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	users.NewHandler(usersService, authenticator, nil).RegisterRoutes(mux)
	return mux, resolver, usersService, membershipsService
}

// newTestServerWithLicenses is newTestServerWithMemberships, but also
// returns the licenses.Service and the underlying tx (needed for
// testutil.LicenseTypeID) — for the one test that needs to assign
// someone a license, since GET /users only shows a plain member
// license_type, not the fuller license history admin/board get.
func newTestServerWithLicenses(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *users.Service, *memberships.Service, *licenses.Service, pgx.Tx) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()

	mux := http.NewServeMux()
	users.NewHandler(usersService, authenticator, nil).RegisterRoutes(mux)
	return mux, resolver, usersService, membershipsService, licensesService, tx
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

// newTestServerWithNotifier is newTestServer, but also wires a
// fakeNotifier so tests can assert on mutation-notification content —
// separate from newTestServer itself so the many tests that don't care
// about notifications aren't slowed down polling for one that'll never
// arrive.
func newTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, *users.Service, *fakeNotifier) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	users.NewHandler(usersService, authenticator, notifier).RegisterRoutes(mux)
	return mux, resolver, usersService, notifier
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
	mux, authn, svc, membershipsService := newTestServerWithMemberships(t)
	ctx := context.Background()

	nationalID := "12345678A"
	// 30 this year, relative to whenever the test actually runs — always
	// lands in the "mayor" (senior, 18-44) age category, unlike a fixed
	// birth year, which would eventually drift into "veterano A" and
	// break the age_category assertion below.
	birthDate := time.Date(time.Now().Year()-30, time.January, 1, 0, 0, 0, 0, time.UTC)
	target, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Target", LastName: "Person", Email: strPtr(testutil.UniqueEmail(t)), NationalID: &nationalID, BirthDate: birthDate,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	// A plain member only ever sees people who are or have been a
	// socio (see Handler.list) — give Target an alta so this test still
	// exercises the field restriction it's actually about.
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: birthDate}); err != nil {
		t.Fatalf("create membership: %v", err)
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
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"age_category":"senior"`)) {
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

// A plain member's own listing includes MemberListing.Roles now (not
// just admin/board's full User) — this is what a "Cargo" column on
// /members is built from client-side.
func TestHandlerListIncludesRolesForAPlainMember(t *testing.T) {
	mux, authn, svc, membershipsService := newTestServerWithMemberships(t)
	ctx := context.Background()

	target, err := svc.Create(ctx, users.CreateInput{FirstName: "Has", LastName: "Roles", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: time.Now().UTC()}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// target now has an open membership period, so "member" (automatic)
	// is already part of their live-computed roles.
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"roles":["member"]`)) {
		t.Errorf("expected a plain member's listing to include roles, got %s", rec.Body.String())
	}
}

// A plain member's own listing includes MemberListing.OnLeave now too —
// so their own "Tipo de socio" column/filter can tell "baja temporal"
// apart from "activo", the same as admin/board's already could.
func TestHandlerListIncludesOnLeaveForAPlainMember(t *testing.T) {
	mux, authn, svc, membershipsService := newTestServerWithMemberships(t)
	ctx := context.Background()

	target, err := svc.Create(ctx, users.CreateInput{FirstName: "On", LastName: "Leave", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	membership, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: time.Now().UTC().AddDate(0, -1, 0)})
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}
	leaveStart := time.Now().UTC().AddDate(0, 0, -1)
	leaveEnd := time.Now().UTC().AddDate(0, 0, 1)
	if _, err := membershipsService.CreateLeave(ctx, memberships.CreateLeaveInput{
		MembershipID: membership.ID, StartDate: leaveStart, EndDate: leaveEnd,
	}); err != nil {
		t.Fatalf("create leave: %v", err)
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"on_leave":true`)) {
		t.Errorf("expected a plain member's listing to include on_leave, got %s", rec.Body.String())
	}
}

// A plain member's own listing includes MemberListing.LicenseType now
// too (just the type — not license_issued_date/license_valid_until,
// which stay admin/board-only).
func TestHandlerListIncludesLicenseTypeForAPlainMember(t *testing.T) {
	mux, authn, svc, membershipsService, licensesService, tx := newTestServerWithLicenses(t)
	ctx := context.Background()

	target, err := svc.Create(ctx, users.CreateInput{FirstName: "Has", LastName: "License", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: target.ID, StartDate: time.Now().UTC()}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	licenseTypeID := testutil.LicenseTypeID(t, tx, "A BASICA")
	if _, err := licensesService.Create(ctx, licenses.CreateInput{
		UserID: target.ID, LicenseTypeID: licenseTypeID, IssuedDate: time.Now().UTC(),
		Year: time.Now().UTC().Year(), TransactionID: testutil.InsertTransaction(t, tx),
	}); err != nil {
		t.Fatalf("create license: %v", err)
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"license_type":"A BASICA"`)) {
		t.Errorf("expected a plain member's listing to include license_type, got %s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("license_issued_date")) {
		t.Errorf("expected a plain member's listing to still hide license_issued_date, got %s", rec.Body.String())
	}
}

// GET /users only ever shows a plain member people who are or have
// been a socio — someone who's never had a membership period at all
// (an admin-only account, say) is hidden from that listing entirely,
// even though admin/board still see them.
func TestHandlerListHidesPeopleWhoHaveNeverBeenAMemberFromAPlainMember(t *testing.T) {
	mux, authn, svc, membershipsService := newTestServerWithMemberships(t)
	ctx := context.Background()

	neverMember, err := svc.Create(ctx, users.CreateInput{FirstName: "Never", LastName: "Member", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create never-member: %v", err)
	}
	pastMember, err := svc.Create(ctx, users.CreateInput{FirstName: "Past", LastName: "Member", Email: strPtr(testutil.UniqueEmail(t))})
	if err != nil {
		t.Fatalf("create past member: %v", err)
	}
	if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: pastMember.ID, StartDate: time.Now().UTC().AddDate(-1, 0, 0)}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	logins := map[string]bool{}
	for _, p := range list {
		logins[p["login"].(string)] = true
	}
	if logins[neverMember.Login] {
		t.Errorf("expected someone who's never been a member to be hidden from a plain member's listing, got %+v", list)
	}
	if !logins[pastMember.Login] {
		t.Errorf("expected a past member to still be visible, got %+v", list)
	}

	adminToken := tokenFor(t, authn, 2, "admin")
	rec = doRequest(mux, http.MethodGet, "/api/v1/users", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(neverMember.Login)) {
		t.Errorf("expected admin to still see someone who's never been a member")
	}
}

// TestHandlerListPaginatesAfterFilteringNonSociosNotBefore covers the
// fix for a previously wrong pagination order: a plain member's
// socio-only restriction used to be applied *after* limit/offset had
// already picked a page, so a page consisting mostly (or entirely) of
// never-a-member accounts — alphabetically ahead of every real socio,
// here — came back short of limit, or even empty, despite plenty of
// real socios existing further along the same ordering. The filter now
// runs in the database, before limit/offset, so a plain member's own
// page is always as full as there are socios left to fill it.
// TestHandlerListReturnsEverySocioUnpaginated covers GET /users being
// deliberately unpaginated (see Service.List's own doc comment —
// membership grows by roughly 15 people a year, nowhere near the scale
// where returning everyone in one response is a real cost): a plain
// member's own listing includes every socio on file, not some subset
// of them, regardless of how many non-socio accounts exist alongside.
func TestHandlerListReturnsEverySocioUnpaginated(t *testing.T) {
	mux, authn, svc, membershipsService := newTestServerWithMemberships(t)
	ctx := context.Background()

	for _, lastName := range []string{"AA", "AB", "AC"} {
		if _, err := svc.Create(ctx, users.CreateInput{FirstName: "Never", LastName: lastName, Email: strPtr(testutil.UniqueEmail(t))}); err != nil {
			t.Fatalf("create never-member %s: %v", lastName, err)
		}
	}
	const socioCount = 6
	for i := 0; i < socioCount; i++ {
		socio, err := svc.Create(ctx, users.CreateInput{FirstName: "Socio", LastName: testutil.UniqueEmail(t), Email: strPtr(testutil.UniqueEmail(t))})
		if err != nil {
			t.Fatalf("create socio %d: %v", i, err)
		}
		if _, err := membershipsService.Create(ctx, memberships.CreateInput{UserID: socio.ID, StartDate: time.Now().UTC().AddDate(-1, 0, 0)}); err != nil {
			t.Fatalf("create membership %d: %v", i, err)
		}
	}

	memberToken := tokenFor(t, authn, 1, "member")
	rec := doRequest(mux, http.MethodGet, "/api/v1/users", memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != socioCount {
		t.Fatalf("expected all %d socios, got %d: %+v", socioCount, len(list), list)
	}
	for _, p := range list {
		if !strings.HasPrefix(p["login"].(string), "socio.") {
			t.Errorf("expected only socios in the response, got %+v", p)
		}
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

// TestHandlerUpdateClearsAFieldSentAsExplicitNullOverHTTP is the
// handler-level version of the reported bug: a PATCH body with
// "phone": null (what the frontend actually sends when someone empties
// the field) must clear it, not be silently equivalent to leaving
// "phone" out of the body entirely.
func TestHandlerUpdateClearsAFieldSentAsExplicitNullOverHTTP(t *testing.T) {
	mux, authn, svc := newTestServer(t)
	ctx := context.Background()

	member, err := svc.Create(ctx, users.CreateInput{
		FirstName: "M", LastName: "Ember", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberToken := tokenFor(t, authn, member.ID, "member")

	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/users/%s", member.Login), memberToken,
		map[string]any{"phone": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	reloaded, err := svc.Get(ctx, member.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Phone != nil {
		t.Errorf("expected phone to be cleared, still got %q", *reloaded.Phone)
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

func TestHandlerCreateNotifiesTelegramWithActorAndNewPerson(t *testing.T) {
	mux, authn, svc, notifier := newTestServerWithNotifier(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Admin", LastName: "Uno", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	body := map[string]any{
		"first_name": "Nueva", "last_name": "Persona", "email": testutil.UniqueEmail(t), "birth_date": "2000-01-01",
	}
	if rec := doRequest(mux, http.MethodPost, "/api/v1/users", adminToken, body); rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) {
		t.Errorf("expected the notification to name the actor %q, got %q", admin.Login, msg)
	}
	if !strings.Contains(msg, "Nueva") || !strings.Contains(msg, "Persona") {
		t.Errorf("expected the notification to name the new person, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff is the core of the
// feature this whole notifier exists for: seeing exactly what changed,
// not just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, authn, svc, notifier := newTestServerWithNotifier(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Admin", LastName: "Dos", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	target, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Juan", LastName: "Perez", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	body := map[string]any{"phone": "666777888"}
	if rec := doRequest(mux, http.MethodPatch, "/api/v1/users/"+target.Login, adminToken, body); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) || !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name both actor and target, got %q", msg)
	}
	if !strings.Contains(msg, "Phone: 611222333 → 666777888") {
		t.Errorf("expected the notification to show the phone change, got %q", msg)
	}
}

// TestHandlerUpdateWithNoActualChangeSendsNoNotification makes sure a
// PATCH that doesn't actually change anything (every field resent with
// its own current value) doesn't produce a misleading "edited" message
// with an empty diff.
func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, authn, svc, notifier := newTestServerWithNotifier(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Admin", LastName: "Tres", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	target, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Sin", LastName: "Cambios", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	body := map[string]any{"phone": "611222333"}
	if rec := doRequest(mux, http.MethodPatch, "/api/v1/users/"+target.Login, adminToken, body); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

func TestHandlerDeleteNotifiesTelegramWithActorAndDeletedPerson(t *testing.T) {
	mux, authn, svc, notifier := newTestServerWithNotifier(t)
	ctx := context.Background()

	admin, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Admin", LastName: "Cuatro", Email: strPtr(testutil.UniqueEmail(t)), RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	target, err := svc.Create(ctx, users.CreateInput{
		FirstName: "Se", LastName: "Borra", Email: strPtr(testutil.UniqueEmail(t)),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	adminToken := tokenFor(t, authn, admin.ID, "admin")

	if rec := doRequest(mux, http.MethodDelete, "/api/v1/users/"+target.Login, adminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, admin.Login) {
		t.Errorf("expected the notification to name the actor %q, got %q", admin.Login, msg)
	}
	if !strings.Contains(msg, target.Login) {
		t.Errorf("expected the notification to name the deleted person %q, got %q", target.Login, msg)
	}
}

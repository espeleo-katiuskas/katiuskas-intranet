package authapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"katiuskas-intranet/backend/internal/auth"
	"katiuskas-intranet/backend/internal/authapi"
	"katiuskas-intranet/backend/internal/httpapi"
	"katiuskas-intranet/backend/internal/memberships"
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
	"katiuskas-intranet/backend/internal/tokens"
	"katiuskas-intranet/backend/internal/users"
)

// capturingMailer stands in for a real mail.Sender so tests can grab the
// magic link straight out of the "email" instead of needing an inbox.
type capturingMailer struct {
	sent             int
	lastTo, lastBody string
	lastSubject      string
}

func (m *capturingMailer) Send(_ context.Context, to, subject, body string) error {
	m.sent++
	m.lastTo, m.lastSubject, m.lastBody = to, subject, body
	return nil
}

type testServer struct {
	mux    *http.ServeMux
	tx     pgx.Tx
	users  *users.Service
	tokens *tokens.Service
	mailer *capturingMailer
}

func newTestServer(t *testing.T) *testServer {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)
	mailer := &capturingMailer{}

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, authapi.Options{
		TokenTTL:          time.Hour,
		MagicLinkTTL:      15 * time.Minute,
		MagicLinkCooldown: time.Minute,
		PublicBaseURL:     "http://localhost:8000",
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return &testServer{mux: mux, tx: tx, users: usersService, tokens: tokensService, mailer: mailer}
}

func (s *testServer) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func (s *testServer) getWithToken(t *testing.T, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

// insertUserWithRole inserts a person and assigns them a role — needed
// for any test that expects a magic link to actually be sent, since
// RequestMagicLink now silently skips anyone with no auth.BaselineRoles
// role at all (signing in would be pointless for them, see
// sendMagicLinkIfEligible).
func (s *testServer) insertUserWithRole(t *testing.T, phone, role string) testutil.TestUser {
	t.Helper()
	user := testutil.InsertUser(t, s.tx, phone)
	rolesService := roles.NewService(roles.NewRepository(s.tx))
	if err := rolesService.AssignByName(context.Background(), user.ID, []string{role}); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	return user
}

var tokenInLink = regexp.MustCompile(`token=([\w-]+)`)

func (s *testServer) requestAndExtractToken(t *testing.T, email, phone string) string {
	t.Helper()
	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": email, "phone": phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("request magic link: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.mailer.sent == 0 {
		t.Fatalf("expected an email to have been sent")
	}
	m := tokenInLink.FindStringSubmatch(s.mailer.lastBody)
	if m == nil {
		t.Fatalf("no token found in email body: %q", s.mailer.lastBody)
	}
	return m[1]
}

func decodeSessionToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode session token from %q: %v", rec.Body.String(), err)
	}
	if body.Token == "" {
		t.Fatalf("expected a non-empty token, got %q", rec.Body.String())
	}
	return body.Token
}

// me hits the protected /auth/me endpoint to learn what identity and
// roles a session token resolves to — the only way to inspect an opaque
// token from the outside, since (unlike the old JWT) there's nothing to
// decode locally.
func (s *testServer) me(t *testing.T, token string) (id int64, roleNames []string, status int) {
	t.Helper()
	rec := s.getWithToken(t, "/api/v1/auth/me", token)
	if rec.Code != http.StatusOK {
		return 0, nil, rec.Code
	}
	var body struct {
		ID    int64    `json:"id"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /auth/me response %q: %v", rec.Body.String(), err)
	}
	return body.ID, body.Roles, rec.Code
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestRequestMagicLinkSendsEmailOnMatch(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "board")

	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	if token == "" {
		t.Fatalf("expected a non-empty token")
	}
}

func TestRequestMagicLinkSkipsSomeoneWithNoBaselineRole(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333")

	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": user.Email, "phone": user.Phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even though no link is sent, got %d", rec.Code)
	}
	if s.mailer.sent != 0 {
		t.Errorf("expected no email for someone with no admin/board/member role, got %d sent", s.mailer.sent)
	}
}

func TestRequestMagicLinkResponseIsIdenticalWhetherOrNotItMatches(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "board")

	match := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": user.Email, "phone": user.Phone})
	wrongPhone := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": user.Email, "phone": "000000000"})
	unknownEmail := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": "nobody@example.com", "phone": "000000000"})

	if match.Code != http.StatusOK || wrongPhone.Code != http.StatusOK || unknownEmail.Code != http.StatusOK {
		t.Fatalf("expected 200 in all three cases, got %d / %d / %d", match.Code, wrongPhone.Code, unknownEmail.Code)
	}
	if match.Body.String() != wrongPhone.Body.String() || match.Body.String() != unknownEmail.Body.String() {
		t.Errorf("expected an identical response body regardless of whether the details matched:\n%q\n%q\n%q",
			match.Body.String(), wrongPhone.Body.String(), unknownEmail.Body.String())
	}
	if s.mailer.sent != 1 {
		t.Errorf("expected exactly 1 email sent (only for the real match), got %d", s.mailer.sent)
	}
}

func TestRequestMagicLinkRequiresBothFields(t *testing.T) {
	s := newTestServer(t)

	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": "someone@example.com"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when phone is missing, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "missing_fields" {
		t.Errorf("expected error code missing_fields, got %q", code)
	}
}

func TestRequestMagicLinkRespectsCooldown(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "board")

	s.requestAndExtractToken(t, user.Email, user.Phone)
	if s.mailer.sent != 1 {
		t.Fatalf("expected 1 email after the first request, got %d", s.mailer.sent)
	}

	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": user.Email, "phone": user.Phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when the cooldown suppresses the email, got %d", rec.Code)
	}
	if s.mailer.sent != 1 {
		t.Errorf("expected the cooldown to suppress a second email, but sent=%d", s.mailer.sent)
	}
}

func TestConsumeMagicLinkIssuesASessionMatchingTheUsersRoles(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")

	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)
	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	sessionToken := decodeSessionToken(t, rec)
	id, roleNames, status := s.me(t, sessionToken)
	if status != http.StatusOK {
		t.Fatalf("expected /auth/me to succeed with the issued token, got %d", status)
	}
	if id != user.ID {
		t.Errorf("expected user id %d, got %d", user.ID, id)
	}
	if len(roleNames) != 1 || roleNames[0] != "admin" {
		t.Errorf("expected roles [admin], got %v", roleNames)
	}
}

func TestConsumeMagicLinkIssuesTheComputedMemberRole(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333")
	if _, err := s.tx.Exec(context.Background(), `
		INSERT INTO memberships (user_id, start_date) VALUES ($1, '2020-01-01')
	`, user.ID); err != nil {
		t.Fatalf("insert active membership: %v", err)
	}

	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)
	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	sessionToken := decodeSessionToken(t, rec)
	_, roleNames, status := s.me(t, sessionToken)
	if status != http.StatusOK {
		t.Fatalf("expected /auth/me to succeed with the issued token, got %d", status)
	}
	if len(roleNames) != 1 || roleNames[0] != "member" {
		t.Errorf("expected roles [member] computed from the active membership, got %v", roleNames)
	}
}

func TestConsumeMagicLinkRejectsInvalidToken(t *testing.T) {
	s := newTestServer(t)
	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": "never-issued"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestConsumeMagicLinkTokenIsSingleUse(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "board")
	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)

	first := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if first.Code != http.StatusOK {
		t.Fatalf("expected the first consumption to succeed, got %d", first.Code)
	}
	second := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if second.Code != http.StatusUnauthorized {
		t.Errorf("expected reusing the token to fail with 401, got %d", second.Code)
	}
}

func TestMeRejectsMissingOrRevokedToken(t *testing.T) {
	s := newTestServer(t)

	if _, _, status := s.me(t, ""); status != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", status)
	}
	if _, _, status := s.me(t, "never-issued"); status != http.StatusUnauthorized {
		t.Errorf("expected 401 for an unknown token, got %d", status)
	}
}

func TestLogoutRevokesTheToken(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "board")

	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)
	sessionToken := decodeSessionToken(t, s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken}))

	if _, _, status := s.me(t, sessionToken); status != http.StatusOK {
		t.Fatalf("expected the fresh token to work, got %d", status)
	}

	if rec := s.post(t, "/api/v1/auth/logout", map[string]string{"token": sessionToken}); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	if _, _, status := s.me(t, sessionToken); status != http.StatusUnauthorized {
		t.Errorf("expected the revoked token to be rejected, got %d", status)
	}
}

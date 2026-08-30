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
	"katiuskas-intranet/backend/internal/roles"
	"katiuskas-intranet/backend/internal/testutil"
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
	auth   *auth.Authenticator
	mailer *capturingMailer
}

func newTestServer(t *testing.T) *testServer {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	usersService := users.NewService(users.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator("test-secret", time.Hour)
	mailer := &capturingMailer{}

	handler := authapi.NewHandler(usersService, rolesService, authenticator, mailer, authapi.Options{
		RefreshTTL:        time.Hour,
		MagicLinkTTL:      15 * time.Minute,
		MagicLinkCooldown: time.Minute,
		PublicBaseURL:     "http://localhost:8000",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/magic-link", handler.RequestMagicLink)
	mux.HandleFunc("POST /api/v1/auth/magic-link/consume", handler.ConsumeMagicLink)
	mux.HandleFunc("POST /api/v1/auth/refresh", handler.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", handler.Logout)

	return &testServer{mux: mux, tx: tx, users: usersService, auth: authenticator, mailer: mailer}
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

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func decodeTokenPair(t *testing.T, rec *httptest.ResponseRecorder) tokenPair {
	t.Helper()
	var pair tokenPair
	if err := json.Unmarshal(rec.Body.Bytes(), &pair); err != nil {
		t.Fatalf("decode token pair from %q: %v", rec.Body.String(), err)
	}
	return pair
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
	user := testutil.InsertUser(t, s.tx, "611222333", true)

	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	if token == "" {
		t.Fatalf("expected a non-empty token")
	}
}

func TestRequestMagicLinkResponseIsIdenticalWhetherOrNotItMatches(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333", true)

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
	user := testutil.InsertUser(t, s.tx, "611222333", true)

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
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	rolesService := roles.NewService(roles.NewRepository(s.tx))
	if err := rolesService.AssignByName(context.Background(), user.ID, []string{"admin"}); err != nil {
		t.Fatalf("assign role: %v", err)
	}

	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	pair := decodeTokenPair(t, rec)
	claims, err := s.auth.ParseAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse issued access token: %v", err)
	}
	if claims.UserID != user.ID {
		t.Errorf("expected user id %d in the token, got %d", user.ID, claims.UserID)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "admin" {
		t.Errorf("expected roles [admin] in the token, got %v", claims.Roles)
	}
}

func TestConsumeMagicLinkIssuesTheComputedMemberRole(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	if _, err := s.tx.Exec(context.Background(), `
		INSERT INTO memberships (user_id, start_date) VALUES ($1, '2020-01-01')
	`, user.ID); err != nil {
		t.Fatalf("insert active membership: %v", err)
	}

	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	pair := decodeTokenPair(t, rec)
	claims, err := s.auth.ParseAccessToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse issued access token: %v", err)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "member" {
		t.Errorf("expected roles [member] computed from the active membership, got %v", claims.Roles)
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
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	token := s.requestAndExtractToken(t, user.Email, user.Phone)

	first := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token})
	if first.Code != http.StatusOK {
		t.Fatalf("expected the first consumption to succeed, got %d", first.Code)
	}
	second := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token})
	if second.Code != http.StatusUnauthorized {
		t.Errorf("expected reusing the token to fail with 401, got %d", second.Code)
	}
}

func TestRefreshRotatesTheToken(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	pair := decodeTokenPair(t, s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token}))

	rec := s.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": pair.RefreshToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	newPair := decodeTokenPair(t, rec)
	if newPair.RefreshToken == pair.RefreshToken {
		t.Errorf("expected refresh to rotate the token, got the same one back")
	}

	// The old refresh token must no longer work.
	reuse := s.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": pair.RefreshToken})
	if reuse.Code != http.StatusUnauthorized {
		t.Errorf("expected reusing a rotated-away refresh token to fail with 401, got %d", reuse.Code)
	}
}

func TestRefreshRejectsDeactivatedAccount(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	pair := decodeTokenPair(t, s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token}))

	if _, err := s.tx.Exec(context.Background(), `UPDATE accounts SET is_active = false WHERE user_id = $1`, user.ID); err != nil {
		t.Fatalf("deactivate account: %v", err)
	}

	rec := s.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": pair.RefreshToken})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected refreshing a deactivated account to fail with 401, got %d", rec.Code)
	}
}

func TestLogoutRevokesTheRefreshToken(t *testing.T) {
	s := newTestServer(t)
	user := testutil.InsertUser(t, s.tx, "611222333", true)
	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	pair := decodeTokenPair(t, s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": token}))

	if rec := s.post(t, "/api/v1/auth/logout", map[string]string{"refresh_token": pair.RefreshToken}); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	rec := s.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": pair.RefreshToken})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected refreshing after logout to fail with 401, got %d", rec.Code)
	}
}

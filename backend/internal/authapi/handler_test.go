package authapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/authapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
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

// capturingNotifier stands in for telegram.Sender so tests can inspect
// what would have been sent, the same way capturingMailer does for
// email. Safe for concurrent use: the handler's own Telegram call runs
// on a goroutine of its own (see Handler.notifyLogin), so a test reading
// these fields has to synchronize with it — see waitForMessage.
type capturingNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (n *capturingNotifier) Send(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, text)
	return nil
}

func (n *capturingNotifier) waitForMessage(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		n.mu.Lock()
		if len(n.sent) > 0 {
			msg := n.sent[0]
			n.mu.Unlock()
			return msg
		}
		n.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a message to be sent")
	return ""
}

// capturingTelegramClient stands in for authapi.TelegramMessenger so
// tests can inspect what DM would have been sent, and to which chat —
// unlike capturingNotifier (the fixed log channel), this one records
// per-chat_id, the way Handler.notifyMagicLinkViaTelegram actually sends.
// nextMessageID hands out increasing, distinguishable ids so a test can
// tell which DeleteMessage call matches which earlier send.
type capturingTelegramClient struct {
	mu   sync.Mutex
	sent []struct {
		chatID    int64
		text      string
		messageID int64
	}
	deleted []struct {
		chatID    int64
		messageID int64
	}
	nextMessageID int64
}

func (c *capturingTelegramClient) SendMessageForID(_ context.Context, chatID int64, text string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextMessageID++
	messageID := c.nextMessageID
	c.sent = append(c.sent, struct {
		chatID    int64
		text      string
		messageID int64
	}{chatID, text, messageID})
	return messageID, nil
}

func (c *capturingTelegramClient) DeleteMessage(_ context.Context, chatID, messageID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, struct {
		chatID    int64
		messageID int64
	}{chatID, messageID})
	return nil
}

func (c *capturingTelegramClient) waitForMessage(t *testing.T) (int64, string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.sent) > 0 {
			msg := c.sent[0]
			c.mu.Unlock()
			return msg.chatID, msg.text
		}
		c.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a Telegram DM to be sent")
	return 0, ""
}

// waitForDelete polls for DeleteMessage to have been called — used by
// tests exercising notifyMagicLinkViaTelegram's own expiry cleanup,
// which sleeps for the handler's configured MagicLinkTTL before
// deleting, so those tests build their testServer with a short TTL
// rather than actually waiting on a real one.
func (c *capturingTelegramClient) waitForDelete(t *testing.T) (int64, int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.deleted) > 0 {
			d := c.deleted[0]
			c.mu.Unlock()
			return d.chatID, d.messageID
		}
		c.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the magic link message to be deleted")
	return 0, 0
}

func (c *capturingTelegramClient) assertNothingSent(t *testing.T) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) != 0 {
		t.Errorf("expected no Telegram DM sent, got %v", c.sent)
	}
}

type testServer struct {
	mux            *http.ServeMux
	handler        *authapi.Handler
	tx             pgx.Tx
	users          *users.Service
	tokens         *tokens.Service
	mailer         *capturingMailer
	notifier       *capturingNotifier
	telegramClient *capturingTelegramClient
}

func newTestServer(t *testing.T) *testServer {
	return newTestServerWithMagicLinkTTL(t, 15*time.Minute)
}

// newTestServerWithMagicLinkTTL is newTestServer, but lets a test pick
// its own MagicLinkTTL and TelegramBotDMLinkTTL (set to the same
// value) — needed for exercising notifyMagicLinkViaTelegram's own
// expiry cleanup, which sleeps for TelegramBotDMLinkTTL before deleting
// the DM it sent; a real 5 minutes would make that test take 5 minutes.
func newTestServerWithMagicLinkTTL(t *testing.T, magicLinkTTL time.Duration) *testServer {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)
	mailer := &capturingMailer{}
	notifier := &capturingNotifier{}
	telegramClient := &capturingTelegramClient{}

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, mailer, notifier, telegramClient, authapi.Options{
		TokenTTL:             time.Hour,
		MagicLinkTTL:         magicLinkTTL,
		MagicLinkCooldown:    time.Minute,
		TelegramBotDMLinkTTL: magicLinkTTL,
		PublicBaseURL:        "http://localhost:64710",
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return &testServer{mux: mux, handler: handler, tx: tx, users: usersService, tokens: tokensService, mailer: mailer, notifier: notifier, telegramClient: telegramClient}
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

// postWithToken is post plus a bearer token — needed for
// /auth/impersonate/exit, unlike every other route in this package,
// which is either unauthenticated (magic-link/logout) or reached via
// getWithToken (/auth/me).
func (s *testServer) postWithToken(t *testing.T, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
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

var codeInBody = regexp.MustCompile(`código en la\s*\n?\s*página de acceso: (\d{6})`)

func (s *testServer) requestAndExtractCode(t *testing.T, email, phone string) string {
	t.Helper()
	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": email, "phone": phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("request magic link: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.mailer.sent == 0 {
		t.Fatalf("expected an email to have been sent")
	}
	m := codeInBody.FindStringSubmatch(s.mailer.lastBody)
	if m == nil {
		t.Fatalf("no code found in email body: %q", s.mailer.lastBody)
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
func (s *testServer) me(t *testing.T, token string) (login string, roleNames []string, status int) {
	t.Helper()
	rec := s.getWithToken(t, "/api/v1/auth/me", token)
	if rec.Code != http.StatusOK {
		return "", nil, rec.Code
	}
	var body struct {
		Login string   `json:"login"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /auth/me response %q: %v", rec.Body.String(), err)
	}
	return body.Login, body.Roles, rec.Code
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
	user := s.insertUserWithRole(t, "611222333", "admin")

	token := s.requestAndExtractToken(t, user.Email, user.Phone)
	if token == "" {
		t.Fatalf("expected a non-empty token")
	}
	if !strings.Contains(s.mailer.lastBody, "página de acceso de la intranet") {
		t.Errorf("expected the email to name the login page as this request's origin, got %q", s.mailer.lastBody)
	}
}

func TestRequestMagicLinkDoesNotDMWhenNoTelegramLinked(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")

	s.requestAndExtractToken(t, user.Email, user.Phone)

	s.telegramClient.assertNothingSent(t)
}

// TestRequestMagicLinkAlsoSendsViaTelegramWhenLinked covers the actual
// feature: someone who's linked their Telegram account gets the same
// link and code by DM too, not just email.
func TestRequestMagicLinkAlsoSendsViaTelegramWhenLinked(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	token := s.requestAndExtractToken(t, user.Email, user.Phone)

	chatID, text := s.telegramClient.waitForMessage(t)
	if chatID != 555 {
		t.Errorf("expected the DM to go to chat_id 555, got %d", chatID)
	}
	if !strings.Contains(text, token) {
		t.Errorf("expected the DM to contain the same token as the email, got %q", text)
	}
	m := codeInBody.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no code found in the Telegram message: %q", text)
	}
	if !strings.Contains(text, "página de acceso de la intranet") {
		t.Errorf("expected the DM to name the login page as this request's origin, got %q", text)
	}
}

// TestIssueMagicLinkForTelegramUserSendsOnlyTheDMNotEmail covers the
// bot's use case directly: someone already recognized who just
// messages the bot again gets the same role/cooldown checks as
// requesting a link from the web, and the link by DM — but, unlike the
// web flow, no email at all: that DM already is the direct reply to
// what they just typed into the bot (see
// TestIssueMagicLinkForTelegramUserDMHasNoCodeOrIgnoreLine for its own
// wording), so a follow-up email would only be redundant.
func TestIssueMagicLinkForTelegramUserSendsOnlyTheDMNotEmail(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	if s.mailer.sent != 0 {
		t.Errorf("expected no email to be sent for a Telegram-bot-triggered request, got %d sent", s.mailer.sent)
	}
	chatID, text := s.telegramClient.waitForMessage(t)
	if chatID != 555 {
		t.Errorf("expected the DM to go to chat_id 555, got %d", chatID)
	}
	if tokenInLink.FindStringSubmatch(text) == nil {
		t.Errorf("expected a sign-in link in the DM, got %q", text)
	}
}

// TestNotifyMagicLinkViaTelegramDeletesTheDMOnceItExpires: the link
// stops working after MagicLinkTTL regardless, but leaving a dead link
// sitting in the chat is misleading, and one more thing exposed if the
// phone/session is compromised later — see notifyMagicLinkViaTelegram's
// own doc comment on why this is a hygiene nicety, not a security gate.
func TestNotifyMagicLinkViaTelegramDeletesTheDMOnceItExpires(t *testing.T) {
	s := newTestServerWithMagicLinkTTL(t, 30*time.Millisecond)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	sentChatID, _ := s.telegramClient.waitForMessage(t)
	deletedChatID, deletedMessageID := s.telegramClient.waitForDelete(t)
	if deletedChatID != sentChatID {
		t.Errorf("expected the delete to target the same chat the DM was sent to, got sent=%d deleted=%d", sentChatID, deletedChatID)
	}
	s.telegramClient.mu.Lock()
	wantMessageID := s.telegramClient.sent[0].messageID
	s.telegramClient.mu.Unlock()
	if deletedMessageID != wantMessageID {
		t.Errorf("expected the delete to target the sent message's own id %d, got %d", wantMessageID, deletedMessageID)
	}
}

// TestNotifyMagicLinkViaTelegramDoesNotDeleteAWebTriggeredDM: only a
// bot-flow DM (viaTelegramBot true, see the test above) is ever
// auto-deleted — one merely riding along a web request has no
// equivalent "already answered its purpose" moment (the code in it
// might still be typed into the login page from a different device
// well after this), so it's left alone indefinitely.
func TestNotifyMagicLinkViaTelegramDoesNotDeleteAWebTriggeredDM(t *testing.T) {
	s := newTestServerWithMagicLinkTTL(t, 30*time.Millisecond)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	s.requestAndExtractToken(t, user.Email, user.Phone)
	s.telegramClient.waitForMessage(t)

	// Comfortably past the short TTL newTestServerWithMagicLinkTTL used
	// above, which would have already triggered a delete by now if this
	// DM were treated as a bot-flow one.
	time.Sleep(100 * time.Millisecond)
	s.telegramClient.mu.Lock()
	deleted := len(s.telegramClient.deleted)
	s.telegramClient.mu.Unlock()
	if deleted != 0 {
		t.Errorf("expected no delete for a web-triggered DM, got %d deletes", deleted)
	}
}

// TestIssueMagicLinkForTelegramUserDMStatesShorterValidity checks the
// bot-flow DM's own wording uses TelegramBotDMLinkTTL, not the real,
// longer MagicLinkTTL every other message states — built with the two
// deliberately different (unlike newTestServerWithMagicLinkTTL, which
// sets them equal for the delete-timing tests above) so a test that
// accidentally read the wrong one would fail.
func TestIssueMagicLinkForTelegramUserDMStatesShorterValidity(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)
	telegramClient := &capturingTelegramClient{}

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, &capturingMailer{}, &capturingNotifier{}, telegramClient, authapi.Options{
		TokenTTL:             time.Hour,
		MagicLinkTTL:         15 * time.Minute,
		MagicLinkCooldown:    time.Minute,
		TelegramBotDMLinkTTL: 5 * time.Minute,
		PublicBaseURL:        "http://localhost:64710",
	})

	user := testutil.InsertUser(t, tx, "611222333")
	if err := rolesService.AssignByName(context.Background(), user.ID, []string{"admin"}); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	_, text := telegramClient.waitForMessage(t)
	if !strings.Contains(text, "válido durante 5 minutos") {
		t.Errorf("expected the bot-flow DM to state TelegramBotDMLinkTTL (5 minutes), got %q", text)
	}
	if strings.Contains(text, "15 minutos") {
		t.Errorf("expected the bot-flow DM not to state the real, longer MagicLinkTTL, got %q", text)
	}
}

// TestIssueMagicLinkForTelegramUserDMHasNoCodeOrIgnoreLine covers the
// bot-flow wording directly: unlike the email (which might be read on
// another device, hence the code-entry fallback) or a DM sent merely
// because a web request happens to also have Telegram linked, this DM
// is the direct reply to a message the person just sent the bot — there
// is no code-entry page open anywhere for it to be useful for, and
// nothing to "not have requested".
func TestIssueMagicLinkForTelegramUserDMHasNoCodeOrIgnoreLine(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	_, text := s.telegramClient.waitForMessage(t)
	if codeInBody.FindStringSubmatch(text) != nil {
		t.Errorf("expected no login code in the bot-flow DM, got %q", text)
	}
	if strings.Contains(text, "Si no has pedido este acceso") {
		t.Errorf("expected no \"didn't request this\" line in the bot-flow DM, got %q", text)
	}
}

// TestIssueMagicLinkForTelegramUserWorksForSomeoneWithNoEmailOnFile
// covers the one case only this Telegram-triggered path can hit:
// resolving straight from a linked chat id, unlike the web flow,
// doesn't require an email on file at all — so this must still work
// (not, say, panic dereferencing a nil user.Email) for someone who has
// none. Since issueMagicLink now skips the email entirely for any
// Telegram-bot-triggered request regardless of whether one is on file,
// this doesn't hit a different code path than
// TestIssueMagicLinkForTelegramUserSendsOnlyTheDMNotEmail, only a
// different fixture — worth keeping anyway as the one test that would
// catch a nil-email dereference if that early return were ever removed.
func TestIssueMagicLinkForTelegramUserWorksForSomeoneWithNoEmailOnFile(t *testing.T) {
	s := newTestServer(t)
	phone := "611222333"
	user, err := s.users.Create(context.Background(), users.CreateInput{
		FirstName: "Sin", LastName: "Correo", Phone: &phone, RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	if s.mailer.sent != 0 {
		t.Errorf("expected no email attempt for someone with none on file, got %d", s.mailer.sent)
	}
	if _, text := s.telegramClient.waitForMessage(t); tokenInLink.FindStringSubmatch(text) == nil {
		t.Errorf("expected the DM to still carry a sign-in link, got %q", text)
	}
}

func TestIssueMagicLinkForTelegramUserSkipsSomeoneWithNoBaselineRole(t *testing.T) {
	s := newTestServer(t)
	phone := "611222333"
	user, err := s.users.Create(context.Background(), users.CreateInput{
		FirstName: "Sin", LastName: "Rol", Email: strPtr(testutil.UniqueEmail(t)), Phone: &phone,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("IssueMagicLinkForTelegramUser: %v", err)
	}

	if s.mailer.sent != 0 {
		t.Errorf("expected no email for someone with no baseline role, got %d", s.mailer.sent)
	}
	s.telegramClient.assertNothingSent(t)
}

func TestIssueMagicLinkForTelegramUserRespectsCooldown(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	if err := s.users.SetTelegramChatID(context.Background(), user.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("first call: %v", err)
	}
	s.telegramClient.waitForMessage(t)
	firstSent := s.mailer.sent

	if err := s.handler.IssueMagicLinkForTelegramUser(context.Background(), user.ID); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if s.mailer.sent != firstSent {
		t.Errorf("expected the cooldown to suppress a second email, but sent went from %d to %d", firstSent, s.mailer.sent)
	}
	time.Sleep(20 * time.Millisecond)
	s.telegramClient.mu.Lock()
	dmCount := len(s.telegramClient.sent)
	s.telegramClient.mu.Unlock()
	if dmCount != 1 {
		t.Errorf("expected the cooldown to suppress a second DM too, got %d DMs sent", dmCount)
	}
}

// A person can register more than one email (see users.User.Email); the
// magic link must go out to all of them, and signing in works with any
// one.
func TestRequestMagicLinkSendsToAllOfSeveralEmails(t *testing.T) {
	s := newTestServer(t)
	primary := testutil.UniqueEmail(t)
	secondary := testutil.UniqueEmail(t)
	phone := "611222333"

	if _, err := s.users.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(primary + " " + secondary), Phone: &phone, RoleNames: []string{"admin"},
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token := s.requestAndExtractToken(t, secondary, phone)
	if token == "" {
		t.Fatalf("expected a non-empty token when logging in with the secondary email")
	}
	if s.mailer.lastTo != primary+", "+secondary {
		t.Errorf("expected the magic link to be sent to both addresses, got %q", s.mailer.lastTo)
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
	user := s.insertUserWithRole(t, "611222333", "admin")

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
	user := s.insertUserWithRole(t, "611222333", "admin")

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

// TestRequestMagicLinkIsRateLimitedPerIPAddress covers the per-address
// throttle (httpapi.IPRateLimiter) wrapping this route in RegisterRoutes
// — a coarser backstop above the per-user cooldown tested above, which
// does nothing to slow down one address cycling through many different
// fake email/phone guesses. newTestServer itself leaves
// MagicLinkIPRateLimit unset (disabled, see Options' own doc comment),
// so this builds its own handler with a small explicit limit instead.
func TestRequestMagicLinkIsRateLimitedPerIPAddress(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, &capturingMailer{}, &capturingNotifier{}, &capturingTelegramClient{}, authapi.Options{
		TokenTTL:              time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     time.Minute,
		PublicBaseURL:         "http://localhost:64710",
		MagicLinkIPRateLimit:  1,
		MagicLinkIPRateWindow: time.Minute,
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	postFrom := func(remoteAddr string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"email": "nobody@example.com", "phone": "600000000"})
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := postFrom("203.0.113.9:1111"); rec.Code != http.StatusOK {
		t.Fatalf("expected 1st request to pass through with 200, got %d", rec.Code)
	}
	rec := postFrom("203.0.113.9:1111")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 2nd request from the same address to get 429, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "rate_limited" {
		t.Errorf("expected error code rate_limited, got %q", code)
	}

	if rec := postFrom("203.0.113.10:2222"); rec.Code != http.StatusOK {
		t.Fatalf("expected a different address's own 1st request to still pass through, got %d", rec.Code)
	}
}

// TestConsumeMagicLinkCodeIsRateLimitedPerIPAddress covers the same
// httpapi.IPRateLimiter wrapping this route too (see RegisterRoutes) —
// without it, an attacker could scan many email/phone combinations
// looking for a match with no throttle at all, since
// FindActiveByEmailAndPhone runs before the per-row attempt counter on
// the code itself is even reached.
func TestConsumeMagicLinkCodeIsRateLimitedPerIPAddress(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, &capturingMailer{}, &capturingNotifier{}, &capturingTelegramClient{}, authapi.Options{
		TokenTTL:              time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     time.Minute,
		PublicBaseURL:         "http://localhost:64710",
		MagicLinkIPRateLimit:  1,
		MagicLinkIPRateWindow: time.Minute,
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	postFrom := func(remoteAddr string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"email": "nobody@example.com", "phone": "600000000", "code": "000000"})
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link/consume-code", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := postFrom("203.0.113.9:1111"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 1st request to pass through to the handler (invalid_code, no such user), got %d", rec.Code)
	}
	rec := postFrom("203.0.113.9:1111")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 2nd request from the same address to get 429, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "rate_limited" {
		t.Errorf("expected error code rate_limited, got %q", code)
	}

	if rec := postFrom("203.0.113.10:2222"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected a different address's own 1st request to still pass through, got %d", rec.Code)
	}
}

// TestMagicLinkRequestAndConsumeCodeShareOneIPRateLimitBudget covers
// that both routes are wrapped by the very same *httpapi.IPRateLimiter
// (see RegisterRoutes and Options' own doc comment) rather than each
// getting its own independent one — exhausting the budget via one
// endpoint blocks the other too, for the same address.
func TestMagicLinkRequestAndConsumeCodeShareOneIPRateLimitBudget(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, &capturingMailer{}, &capturingNotifier{}, &capturingTelegramClient{}, authapi.Options{
		TokenTTL:              time.Hour,
		MagicLinkTTL:          15 * time.Minute,
		MagicLinkCooldown:     time.Minute,
		PublicBaseURL:         "http://localhost:64710",
		MagicLinkIPRateLimit:  1,
		MagicLinkIPRateWindow: time.Minute,
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	const remoteAddr = "203.0.113.9:1111"

	linkBody, err := json.Marshal(map[string]string{"email": "nobody@example.com", "phone": "600000000"})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	linkReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link", bytes.NewReader(linkBody))
	linkReq.Header.Set("Content-Type", "application/json")
	linkReq.RemoteAddr = remoteAddr
	linkRec := httptest.NewRecorder()
	mux.ServeHTTP(linkRec, linkReq)
	if linkRec.Code != http.StatusOK {
		t.Fatalf("expected the 1st request (to /magic-link) to pass through with 200, got %d", linkRec.Code)
	}

	codeBody, err := json.Marshal(map[string]string{"email": "nobody@example.com", "phone": "600000000", "code": "000000"})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	codeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/magic-link/consume-code", bytes.NewReader(codeBody))
	codeReq.Header.Set("Content-Type", "application/json")
	codeReq.RemoteAddr = remoteAddr
	codeRec := httptest.NewRecorder()
	mux.ServeHTTP(codeRec, codeReq)
	if codeRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the same address's 2nd request (to /magic-link/consume-code) to get 429, got %d", codeRec.Code)
	}
	if code := errorCode(t, codeRec); code != "rate_limited" {
		t.Errorf("expected error code rate_limited, got %q", code)
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
	login, roleNames, status := s.me(t, sessionToken)
	if status != http.StatusOK {
		t.Fatalf("expected /auth/me to succeed with the issued token, got %d", status)
	}
	if login != user.Login {
		t.Errorf("expected user login %q, got %q", user.Login, login)
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

func TestConsumeMagicLinkNotifiesTelegramOfTheLogin(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)

	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	msg := s.notifier.waitForMessage(t)
	if !strings.Contains(msg, user.Login) {
		t.Errorf("expected the notification to mention %q, got %q", user.Login, msg)
	}
}

// TestConsumeMagicLinkSetsSessionCookie checks the same session token
// returned in the JSON body is also set as auth.SessionCookieName —
// the transport GET /library/file and GET /users/{login}/files/file
// accept in addition to the bearer header, so a document can be opened
// as a plain, browser-navigable URL. See auth.SetSessionCookie.
func TestConsumeMagicLinkSetsSessionCookie(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)

	rec := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	sessionToken := decodeSessionToken(t, rec)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName {
		t.Fatalf("expected a single %s cookie, got %v", auth.SessionCookieName, cookies)
	}
	if cookies[0].Value != sessionToken {
		t.Errorf("expected the cookie to carry the same token as the response body, got %q vs %q", cookies[0].Value, sessionToken)
	}
	if !cookies[0].HttpOnly {
		t.Errorf("expected the session cookie to be HttpOnly")
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
	user := s.insertUserWithRole(t, "611222333", "admin")
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

func TestConsumeMagicLinkCodeIssuesASession(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")

	code := s.requestAndExtractCode(t, user.Email, user.Phone)
	rec := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{
		"email": user.Email, "phone": user.Phone, "code": code,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	sessionToken := decodeSessionToken(t, rec)
	login, _, status := s.me(t, sessionToken)
	if status != http.StatusOK {
		t.Fatalf("expected /auth/me to succeed with the issued token, got %d", status)
	}
	if login != user.Login {
		t.Errorf("expected user login %q, got %q", user.Login, login)
	}
}

func TestConsumeMagicLinkCodeIsSingleUseAndAlsoBurnsTheLink(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")

	rec := s.post(t, "/api/v1/auth/magic-link", map[string]string{"email": user.Email, "phone": user.Phone})
	if rec.Code != http.StatusOK {
		t.Fatalf("request magic link: %d: %s", rec.Code, rec.Body.String())
	}
	m := codeInBody.FindStringSubmatch(s.mailer.lastBody)
	if m == nil {
		t.Fatalf("no code found in email body: %q", s.mailer.lastBody)
	}
	code := m[1]
	linkToken := tokenInLink.FindStringSubmatch(s.mailer.lastBody)[1]

	first := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{
		"email": user.Email, "phone": user.Phone, "code": code,
	})
	if first.Code != http.StatusOK {
		t.Fatalf("expected the first consumption to succeed, got %d: %s", first.Code, first.Body.String())
	}

	second := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{
		"email": user.Email, "phone": user.Phone, "code": code,
	})
	if second.Code != http.StatusUnauthorized {
		t.Errorf("expected reusing the code to fail with 401, got %d", second.Code)
	}

	linkAfter := s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken})
	if linkAfter.Code != http.StatusUnauthorized {
		t.Errorf("expected the link to be consumed too once the code was used, got %d", linkAfter.Code)
	}
}

func TestConsumeMagicLinkCodeRejectsWrongCode(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	s.requestAndExtractCode(t, user.Email, user.Phone)

	rec := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{
		"email": user.Email, "phone": user.Phone, "code": "000000",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for a wrong code, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "invalid_code" {
		t.Errorf("expected error code invalid_code, got %q", code)
	}
}

// Responses must look identical (401 invalid_code) whether the email
// simply doesn't match anyone or the code is wrong for a real match —
// same anti-enumeration reasoning as the request endpoint.
func TestConsumeMagicLinkCodeRejectsUnknownEmailWithTheSameError(t *testing.T) {
	s := newTestServer(t)
	rec := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{
		"email": "nobody@example.com", "phone": "000000000", "code": "123456",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "invalid_code" {
		t.Errorf("expected error code invalid_code, got %q", code)
	}
}

func TestConsumeMagicLinkCodeRequiresAllFields(t *testing.T) {
	s := newTestServer(t)
	rec := s.post(t, "/api/v1/auth/magic-link/consume-code", map[string]string{"email": "a@example.com", "phone": "611222333"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when code is missing, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "missing_fields" {
		t.Errorf("expected error code missing_fields, got %q", code)
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

func TestMeReportsImpersonation(t *testing.T) {
	s := newTestServer(t)
	admin := s.insertUserWithRole(t, "611222333", "admin")
	target := s.insertUserWithRole(t, "622333444", "admin")

	raw, err := s.tokens.Impersonate(context.Background(), admin.ID, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("impersonate: %v", err)
	}

	rec := s.getWithToken(t, "/api/v1/auth/me", raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Login          string `json:"login"`
		ImpersonatedBy *struct {
			Login string `json:"login"`
		} `json:"impersonated_by"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /auth/me response %q: %v", rec.Body.String(), err)
	}
	if body.Login != target.Login {
		t.Errorf("expected login %q (the target), got %q", target.Login, body.Login)
	}
	if body.ImpersonatedBy == nil || body.ImpersonatedBy.Login != admin.Login {
		t.Fatalf("expected impersonated_by.login %q, got %v", admin.Login, body.ImpersonatedBy)
	}
}

func TestMeOmitsImpersonatedByForANormalSession(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	raw, err := s.tokens.Issue(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec := s.getWithToken(t, "/api/v1/auth/me", raw)
	if bytes.Contains(rec.Body.Bytes(), []byte("impersonated_by")) {
		t.Errorf("expected no impersonated_by for a normal session, got %s", rec.Body.String())
	}
}

func TestExitImpersonationEndsTheSessionAndRestoresTheAdmin(t *testing.T) {
	s := newTestServer(t)
	admin := s.insertUserWithRole(t, "611222333", "admin")
	target := s.insertUserWithRole(t, "622333444", "admin")

	impersonationToken, err := s.tokens.Impersonate(context.Background(), admin.ID, target.ID, time.Hour)
	if err != nil {
		t.Fatalf("impersonate: %v", err)
	}

	rec := s.postWithToken(t, "/api/v1/auth/impersonate/exit", impersonationToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Token == "" || body.Token == impersonationToken {
		t.Fatalf("expected a fresh, non-empty token, got %q", body.Token)
	}

	if _, _, status := s.me(t, impersonationToken); status != http.StatusUnauthorized {
		t.Errorf("expected the impersonation token to be revoked, got status %d", status)
	}
	login, _, status := s.me(t, body.Token)
	if status != http.StatusOK {
		t.Fatalf("expected the new token to work, got %d", status)
	}
	if login != admin.Login {
		t.Errorf("expected the new token to resolve to the admin %q, got %q", admin.Login, login)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName || cookies[0].Value != body.Token {
		t.Errorf("expected the session cookie to be refreshed to the new token, got %v", cookies)
	}
}

func TestExitImpersonationRejectsANormalSession(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	raw, err := s.tokens.Issue(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec := s.postWithToken(t, "/api/v1/auth/impersonate/exit", raw, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 exiting a normal (non-impersonation) session, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExitImpersonationRequiresAuthentication(t *testing.T) {
	s := newTestServer(t)
	rec := s.postWithToken(t, "/api/v1/auth/impersonate/exit", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

func TestLogoutRevokesTheToken(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")

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

// TestLogoutClearsSessionCookie checks logout also expires
// auth.SessionCookieName (MaxAge < 0), not just revoking the token in
// the body — otherwise a signed-out browser could still open a
// library/userfiles file link straight from history or a bookmark.
func TestLogoutClearsSessionCookie(t *testing.T) {
	s := newTestServer(t)
	user := s.insertUserWithRole(t, "611222333", "admin")
	linkToken := s.requestAndExtractToken(t, user.Email, user.Phone)
	sessionToken := decodeSessionToken(t, s.post(t, "/api/v1/auth/magic-link/consume", map[string]string{"token": linkToken}))

	rec := s.post(t, "/api/v1/auth/logout", map[string]string{"token": sessionToken})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName {
		t.Fatalf("expected a %s cookie in the response, got %v", auth.SessionCookieName, cookies)
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("expected a negative MaxAge to clear the cookie, got %d", cookies[0].MaxAge)
	}
}

func strPtr(s string) *string { return &s }

// TestTelegramBotInfoEmptyWhenNotConfigured covers the default case
// (no TELEGRAM_BOT_USERNAME set): the login page must be able to tell
// "Telegram sign-in isn't offered at all" apart from "here's who to
// message", not just get an empty string it can't distinguish from a
// slow network.
func TestTelegramBotInfoEmptyWhenNotConfigured(t *testing.T) {
	s := newTestServer(t)

	rec := s.getWithToken(t, "/api/v1/auth/telegram-bot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Username != "" {
		t.Errorf("expected an empty username, got %q", body.Username)
	}
}

// TestTelegramBotInfoReturnsConfiguredUsername covers the configured
// case — this is a plain, unauthenticated GET (no token at all), since
// the whole point is helping someone who isn't signed in yet.
func TestTelegramBotInfoReturnsConfiguredUsername(t *testing.T) {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	tokensService := tokens.NewService(tokens.NewRepository(tx), rolesService)
	authenticator := auth.NewAuthenticator(tokensService)

	handler := authapi.NewHandler(usersService, rolesService, tokensService, authenticator, &capturingMailer{}, &capturingNotifier{}, &capturingTelegramClient{}, authapi.Options{
		TokenTTL:            time.Hour,
		MagicLinkTTL:        15 * time.Minute,
		MagicLinkCooldown:   time.Minute,
		PublicBaseURL:       "http://localhost:64710",
		TelegramBotUsername: "KatiuskasBot",
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/telegram-bot", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Username != "KatiuskasBot" {
		t.Errorf("expected the configured username, got %q", body.Username)
	}
}

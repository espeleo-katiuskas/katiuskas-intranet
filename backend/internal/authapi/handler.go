// Package authapi exposes the public session endpoints: request a
// magic-link sign-in email, consume that link, and log out. There are no
// passwords anywhere in this API, and no separate refresh token either —
// signing in issues one opaque, revocable token that's used directly on
// every request until it's revoked or expires.
package authapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/mail"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/tokens"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

// Options are the settings that shape magic-link emails: how long a link
// lives, how soon another one can be requested for the same person, how
// long a freshly issued session token lasts before an admin needs to
// extend it, and where the frontend is served (to build the link's URL).
// TelegramBotDMLinkTTL is the shorter, cosmetic validity/deletion-delay
// used only for a bot-flow DM (see notifyMagicLinkViaTelegram) — every
// other message still states, and is bound by, the real MagicLinkTTL.
// TelegramBotUsername is separate from all of that — it's not secret
// (unlike TELEGRAM_BOT_TOKEN, which internal/telegram alone ever sees),
// just the public "@name" a t.me/<username> link needs, so the login
// page can offer signing in via Telegram at all; see TelegramBotInfo.
type Options struct {
	TokenTTL             time.Duration
	MagicLinkTTL         time.Duration
	MagicLinkCooldown    time.Duration
	TelegramBotDMLinkTTL time.Duration
	PublicBaseURL        string
	TelegramBotUsername  string
	// MagicLinkIPRateLimit/MagicLinkIPRateWindow configure the
	// per-address throttle shared by RequestMagicLink and
	// ConsumeMagicLinkCode (see httpapi.NewIPRateLimiter — the same
	// limiter/budget covers both, rather than each getting its own) —
	// left unset, MagicLinkIPRateLimit's zero value disables it
	// outright, which is what every existing test's newTestServer
	// helper gets today.
	MagicLinkIPRateLimit  int
	MagicLinkIPRateWindow time.Duration
}

// TelegramMessenger sends a DM to a specific Telegram chat — unlike
// notifier (telegram.Sender), which always targets the fixed log
// channel, this targets whichever chat_id a specific person has linked
// (see users.Repository.TelegramChatIDForUser). *telegram.Client
// satisfies this structurally; defined locally, the same narrow-
// dependency pattern as MutationNotifier/UserLookup elsewhere, so this
// package doesn't need to import internal/telegram just for this one
// method (it already does, for Sender, but the principle is the same).
// SendMessageForID/DeleteMessage (rather than plain SendMessage) exist
// so notifyMagicLinkViaTelegram can clean up its own DM once the link
// in it expires — see there.
type TelegramMessenger interface {
	SendMessageForID(ctx context.Context, chatID int64, text string) (messageID int64, err error)
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
}

type Handler struct {
	users          *users.Service
	roles          *roles.Service
	tokens         *tokens.Service
	authenticator  *auth.Authenticator
	mailer         mail.Sender
	notifier       telegram.Sender
	telegramClient TelegramMessenger
	opts           Options
	ipLimiter      *httpapi.IPRateLimiter

	pendingTelegramDMsMu sync.Mutex
	pendingTelegramDMs   map[int64]pendingTelegramDM
}

// pendingTelegramDM is the bot-flow magic-link DM (see
// notifyMagicLinkViaTelegram's own viaTelegramBot branch) still waiting
// out its scheduled expiry cleanup for one particular user, tracked so
// issueSessionAndRespond can delete it immediately once that person
// actually signs in rather than leaving it for the full
// Options.TelegramBotDMLinkTTL to elapse. cancel stops that scheduled
// cleanup's own goroutine from also trying to delete the same message
// afterward.
type pendingTelegramDM struct {
	chatID    int64
	messageID int64
	cancel    context.CancelFunc
}

func NewHandler(usersService *users.Service, rolesService *roles.Service, tokensService *tokens.Service, authenticator *auth.Authenticator, mailer mail.Sender, notifier telegram.Sender, telegramClient TelegramMessenger, opts Options) *Handler {
	return &Handler{
		users:              usersService,
		roles:              rolesService,
		tokens:             tokensService,
		authenticator:      authenticator,
		mailer:             mailer,
		notifier:           notifier,
		telegramClient:     telegramClient,
		opts:               opts,
		ipLimiter:          httpapi.NewIPRateLimiter(opts.MagicLinkIPRateLimit, opts.MagicLinkIPRateWindow),
		pendingTelegramDMs: make(map[int64]pendingTelegramDM),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/auth/magic-link", h.ipLimiter.Limit(h.RequestMagicLink))
	mux.HandleFunc("POST /api/v1/auth/magic-link/consume", h.ConsumeMagicLink)
	// Also rate-limited, unlike the link itself: the link's token is 128
	// bits, long enough that entropy alone carries its security (see
	// docs/architecture.md's own magic_link_tokens section) with no
	// per-row attempt counter at all — but this endpoint identifies its
	// caller by email+phone first (see ConsumeMagicLinkCode below),
	// before its own per-row attempt counter on the short 6-digit code
	// is even in play. Without this, an attacker could scan many
	// email/phone combinations for a match with no throttle whatsoever,
	// unlike guessing one known person's code, which the attempt
	// counter already bounds.
	mux.Handle("POST /api/v1/auth/magic-link/consume-code", h.ipLimiter.Limit(h.ConsumeMagicLinkCode))
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)
	mux.HandleFunc("GET /api/v1/auth/telegram-bot", h.TelegramBotInfo)
	mux.Handle("GET /api/v1/auth/me", h.authenticator.Protect(h.Me))
	// RequireAuth only, not Protect: exiting impersonation must work even
	// if the impersonated person has since lost their baseline role (a
	// lapsed membership, say) — otherwise that admin would be stuck
	// impersonating someone whose session can no longer call anything,
	// including this.
	mux.Handle("POST /api/v1/auth/impersonate/exit", h.authenticator.RequireAuth(http.HandlerFunc(h.ExitImpersonation)))
}

// RequestMagicLink always answers the same way regardless of whether
// email+phone matched anyone, whether that person holds a role that
// could actually use the API (see auth.BaselineRoles), or whether a
// link was actually sent (a recent one might already be on its way) —
// telling those cases apart would let this endpoint be used to check
// who's in the system, or to spam a real person's inbox.
func (h *Handler) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Phone) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "email and phone are both required")
		return
	}

	if err := h.sendMagicLinkIfEligible(r.Context(), body.Email, body.Phone); err != nil {
		slog.Error("magic link request failed", "error", err)
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "If those details match someone in our records, an email with a sign-in link is on its way.",
	})
}

// TelegramBotInfo tells the login page whether signing in via Telegram
// is even worth offering, and if so, who to message — a plain GET with
// no auth (the whole point is helping someone who isn't signed in yet),
// no request body, and nothing secret in the response: a bot's
// username is exactly as public as the t.me/<username> link built from
// it. An empty username (Telegram not configured at all) means the
// login page simply doesn't show that option.
func (h *Handler) TelegramBotInfo(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{
		"username": h.opts.TelegramBotUsername,
	})
}

func (h *Handler) sendMagicLinkIfEligible(ctx context.Context, email, phone string) error {
	user, err := h.users.FindActiveByEmailAndPhone(ctx, email, phone)
	if errors.Is(err, users.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return h.issueMagicLink(ctx, user, false)
}

// IssueMagicLinkForTelegramUser replies to a message from someone whose
// Telegram chat is already linked (see internal/telegrambot) with a
// fresh sign-in link, exactly as if they'd requested one from the web —
// same eligibility checks, same delivery. Silently does nothing when
// ineligible (no baseline role, or a recent link is still live), the
// same anti-enumeration-flavored silence sendMagicLinkIfEligible itself
// uses for the web endpoint — there's nothing to hide here (the caller
// already knows exactly who this is), but there's also no reason to
// answer a rate-limited retry any differently than the web flow would.
func (h *Handler) IssueMagicLinkForTelegramUser(ctx context.Context, userID int64) error {
	user, err := h.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	return h.issueMagicLink(ctx, user, true)
}

// requestOrigin is the fixed "recibes este mensaje porque..." clause
// both the email and the web-triggered Telegram DM (see
// notifyMagicLinkViaTelegram) add, naming RequestMagicLink's web form
// as where the request came from — accurate for both, since neither is
// ever built for a Telegram-bot-triggered request (see issueMagicLink,
// notifyMagicLinkViaTelegram's own viaTelegramBot branch): that one
// already gets its answer as the bot's own direct reply, with nothing
// else sent alongside it to carry this line at all.
const requestOrigin = "desde la página de acceso de la intranet"

// issueMagicLink is sendMagicLinkIfEligible/IssueMagicLinkForTelegramUser's
// shared core, once the target person is already resolved: the
// baseline-role and cooldown gates, creating the token/code, and
// delivering it by every channel available for them — except a
// Telegram-bot-triggered request (viaTelegramBot), which stops after
// the DM: that message *is* the direct reply to what they just typed
// into the bot, so a follow-up email would be redundant rather than
// helpful, the same reasoning notifyMagicLinkViaTelegram's own
// viaTelegramBot branch already applies to that DM's own wording.
func (h *Handler) issueMagicLink(ctx context.Context, user users.User, viaTelegramBot bool) error {
	// Signing in would be pointless for someone who can't call the API
	// anyway (see auth.BaselineRoles) — don't hand out a link, and don't
	// even let the cooldown check below reveal that this person exists
	// but has no baseline role.
	roleNames, err := h.roles.ListNamesForUser(ctx, user.ID)
	if err != nil {
		return err
	}
	if !auth.HasBaselineRole(roleNames) {
		return nil
	}

	recently, err := h.users.HasRecentMagicLinkToken(ctx, user.ID, time.Now().Add(-h.opts.MagicLinkCooldown))
	if err != nil {
		return err
	}
	if recently {
		return nil
	}

	rawToken, err := auth.NewMagicLinkToken()
	if err != nil {
		return err
	}
	code, err := auth.NewLoginCode()
	if err != nil {
		return err
	}
	if err := h.users.CreateMagicLinkToken(ctx, user.ID, auth.HashToken(rawToken), auth.HashToken(code), time.Now().Add(h.opts.MagicLinkTTL)); err != nil {
		return err
	}

	link := fmt.Sprintf("%s/login?token=%s", h.opts.PublicBaseURL, rawToken)
	// notifyMagicLinkViaTelegram only ever returns a non-nil error for a
	// bot-triggered request (see its own doc comment) — a web request's
	// own DM attempt is best-effort and always reports success here
	// regardless of what actually happened, since the email below is
	// its real fallback either way. For a bot-triggered one, that DM
	// *is* the only channel this request gets, so a failure here has to
	// reach the caller instead of vanishing: without this, whoever just
	// proved their email to the bot got neither the link nor any word
	// that something went wrong.
	if err := h.notifyMagicLinkViaTelegram(ctx, user.ID, user.FirstName, link, code, viaTelegramBot); err != nil {
		return err
	}

	// A Telegram-bot-triggered request already got its one answer as
	// that DM above — see this function's own doc comment.
	if viaTelegramBot {
		return nil
	}

	// Past the viaTelegramBot return above, this only ever runs for
	// sendMagicLinkIfEligible's own web path, whose FindActiveByEmailAndPhone
	// lookup already guarantees a non-nil user.Email — nothing left here
	// that could reach this point without one.
	subject := "Accede a la intranet del Club de Espeleología Katiuskas"
	body := fmt.Sprintf(
		"Hola %s,\n\n"+
			"Entra en la intranet del Club de Espeleología Katiuskas con este enlace, válido durante %d minutos:\n%s\n\n"+
			"También puedes escribir este código en la página de acceso: %s\n\n"+
			"Recibes este mensaje porque alguien ha solicitado este acceso %s. Si no has sido tú, puedes ignorar este mensaje.",
		user.FirstName, int(h.opts.MagicLinkTTL.Minutes()), link, code, requestOrigin,
	)
	// user.Email may hold more than one, space-separated (see
	// users.User.Email); mail.Sender expects a comma-separated list.
	return h.mailer.Send(ctx, strings.Join(strings.Fields(*user.Email), ", "), subject, body)
}

// errMagicLinkTelegramDMFailed is notifyMagicLinkViaTelegram's own
// sentinel for "this bot-triggered request's one and only channel
// didn't go through" — never returned for a web-triggered request (see
// that function's own doc comment), where a DM is only ever a bonus
// alongside the real, unaffected email fallback.
var errMagicLinkTelegramDMFailed = errors.New("could not deliver the magic link via telegram")

// notifyMagicLinkViaTelegram DMs a sign-in link to whoever has linked
// their Telegram account (see users.Repository.TelegramChatIDForUser).
// When viaTelegramBot is false, this is alongside the email, not
// instead of it: someone with no Telegram linked, or if the lookup or
// the send itself fails, still has the email to fall back on, so this
// always reports success (nil) regardless of what actually happened —
// the chat id lookup runs synchronously on the request's own context,
// same reasoning as notifyLogin (a background goroutine touching the
// database independently of the request isn't safe against a
// request-scoped pgx.Tx), but the network call to Telegram itself, and
// any failure in it, stays fire-and-forget in its own goroutine exactly
// as before. When viaTelegramBot is true, this DM is the *only* thing
// sent at all — issueMagicLink skips the email entirely in that case,
// since this message already is the direct reply to what they just
// typed into the bot — so there's no fallback left to lean on if it
// doesn't go through, and every step (the chat id lookup, and now the
// send itself too) runs synchronously and reports
// errMagicLinkTelegramDMFailed on any failure, all the way back to
// Bot.handleRecognizedChat, which needs to know to actually tell the
// person something went wrong instead of leaving them with silence.
// Bot.Run already processes one update at a time with no goroutine of
// its own, calling the Telegram API directly and synchronously the
// same way elsewhere (SendMessage, SendMessageRequestingContact,
// DeleteMessage) — this is no different, just one more such call.
//
// viaTelegramBot also picks the wording: when this link was requested
// through internal/telegrambot's own conversation (the person just
// typed their email to the bot, see handleRecognizedChat), the message
// leaves out both the login code — there's no code-entry page open
// anywhere for this request, unlike an email, which might be read on a
// different device than the one signing in — and the "if you didn't
// request this, ignore it" line, since this message *is* the direct
// reply to what they just sent the bot; its own stated validity is
// h.opts.TelegramBotDMLinkTTL (see Options), not the real
// h.opts.MagicLinkTTL every other message states. Otherwise (a web request that merely
// happens to also have Telegram linked) the DM matches the email in
// full, code and real TTL included, since either could be what's read
// first — and, unlike the bot-flow DM, is never auto-deleted at all:
// only a bot-flow DM's own delivery is tied to something this handler
// itself schedules a cleanup for.
func (h *Handler) notifyMagicLinkViaTelegram(ctx context.Context, userID int64, firstName, link, code string, viaTelegramBot bool) error {
	fail := func(logMsg string, err error) error {
		slog.Error(logMsg, "error", err)
		if viaTelegramBot {
			return errMagicLinkTelegramDMFailed
		}
		return nil
	}

	if h.telegramClient == nil {
		// Shouldn't happen for a bot-triggered request in practice —
		// internal/telegrambot.Bot only ever runs at all when Telegram
		// is configured (see cmd/api/main.go's telegramConfigured gate)
		// — but still worth failing loudly rather than silently for a
		// path with no email fallback to fall back on.
		if viaTelegramBot {
			return fail("telegram not configured for a bot-triggered magic link", errors.New("telegramClient is nil"))
		}
		return nil
	}
	chatID, err := h.users.TelegramChatIDForUser(ctx, userID)
	if err != nil {
		return fail("look up telegram chat id for magic link", err)
	}
	if chatID == nil {
		// Shouldn't happen for a bot-triggered request either — that
		// flow only ever reaches here once this same chat has already
		// been resolved to this userID (see Bot.handle) — but the same
		// reasoning as the nil-client case above applies.
		if viaTelegramBot {
			return fail("no linked telegram chat for a bot-triggered magic link", errors.New("TelegramChatIDForUser returned nil"))
		}
		return nil
	}
	var text string
	if viaTelegramBot {
		text = fmt.Sprintf(
			"Hola %s,\n\n"+
				"Aquí tienes tu enlace de acceso a la intranet del Club de Espeleología Katiuskas, válido durante %d minutos:\n%s",
			firstName, int(h.opts.TelegramBotDMLinkTTL.Minutes()), link,
		)
	} else {
		text = fmt.Sprintf(
			"Hola %s,\n\n"+
				"Aquí tienes tu enlace de acceso a la intranet del Club de Espeleología Katiuskas, válido durante %d minutos:\n%s\n\n"+
				"También puedes escribir este código en la página de acceso: %s\n\n"+
				"Recibes este mensaje porque alguien ha solicitado este acceso %s. Si no has sido tú, puedes ignorar este mensaje.",
			firstName, int(h.opts.MagicLinkTTL.Minutes()), link, code, requestOrigin,
		)
	}

	send := func() (int64, error) {
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return h.telegramClient.SendMessageForID(sendCtx, *chatID, text)
	}
	// The link itself keeps working for the rest of the real
	// h.opts.MagicLinkTTL regardless of whether this delete succeeds or
	// even runs at all (a process restart in between loses it, same as
	// any other best-effort cleanup in this app) — this is purely a
	// hygiene nicety, not something the link's own security depends on:
	// a dead link lingering in the chat is misleading, and one more
	// thing exposed for no reason if the phone/session is compromised
	// later. The delay before deleting matches h.opts.TelegramBotDMLinkTTL,
	// this message's own (shorter, cosmetic) stated validity — not the
	// real, longer h.opts.MagicLinkTTL, unless issueSessionAndRespond
	// (see deletePendingTelegramDM) fires this early instead, once the
	// person this DM was sent to has actually signed in. Auto-deleting
	// only ever applies to a bot-flow DM — this message's own reason for
	// existing was answering what was just typed into this same chat, so
	// once that's served its purpose there's nothing left worth keeping
	// around; a DM merely riding along a web request has no equivalent
	// moment where it's definitely done being useful (the code in it,
	// especially, might still be typed into the login page from a
	// different device well after this), so it's left alone
	// indefinitely — scheduleCleanup is therefore only ever called from
	// the viaTelegramBot branch below.
	scheduleCleanup := func(messageID int64) {
		cleanupCtx, cancel := context.WithCancel(context.Background())
		h.setPendingTelegramDM(userID, *chatID, messageID, cancel)
		go func() {
			select {
			case <-time.After(h.opts.TelegramBotDMLinkTTL):
			case <-cleanupCtx.Done():
				// Canceled by deletePendingTelegramDM, which already
				// deletes the message itself once canceling it.
				return
			}
			deleteCtx, timeoutCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer timeoutCancel()
			if err := h.telegramClient.DeleteMessage(deleteCtx, *chatID, messageID); err != nil {
				slog.Error("delete expired magic link message", "error", err)
			}
			h.clearPendingTelegramDM(userID, messageID)
		}()
	}

	if viaTelegramBot {
		messageID, err := send()
		if err != nil {
			return fail("send magic link via telegram", err)
		}
		scheduleCleanup(messageID)
		return nil
	}

	go func() {
		if _, err := send(); err != nil {
			slog.Error("send magic link via telegram", "error", err)
		}
	}()
	return nil
}

func (h *Handler) setPendingTelegramDM(userID, chatID, messageID int64, cancel context.CancelFunc) {
	h.pendingTelegramDMsMu.Lock()
	defer h.pendingTelegramDMsMu.Unlock()
	h.pendingTelegramDMs[userID] = pendingTelegramDM{chatID: chatID, messageID: messageID, cancel: cancel}
}

// clearPendingTelegramDM drops the tracked entry once its own scheduled
// cleanup has actually run — guarded by messageID so it can't clear out
// a newer entry that's since replaced it (e.g. a second bot-flow link
// requested before the first one's own timer fired).
func (h *Handler) clearPendingTelegramDM(userID, messageID int64) {
	h.pendingTelegramDMsMu.Lock()
	defer h.pendingTelegramDMsMu.Unlock()
	if pending, ok := h.pendingTelegramDMs[userID]; ok && pending.messageID == messageID {
		delete(h.pendingTelegramDMs, userID)
	}
}

// deletePendingTelegramDM immediately deletes whatever bot-flow
// magic-link DM (see notifyMagicLinkViaTelegram) is still pending for
// userID, and cancels its scheduled expiry cleanup so that goroutine
// doesn't also try to delete the same, already-gone message once
// Options.TelegramBotDMLinkTTL elapses. Called once a sign-in actually
// succeeds (see issueSessionAndRespond), so the message disappears from
// the chat right away instead of sitting there for the rest of that
// TTL. A no-op for every sign-in that isn't the direct result of a
// bot-flow DM — most of them — since there's nothing tracked to delete.
func (h *Handler) deletePendingTelegramDM(userID int64) {
	h.pendingTelegramDMsMu.Lock()
	pending, ok := h.pendingTelegramDMs[userID]
	if ok {
		delete(h.pendingTelegramDMs, userID)
	}
	h.pendingTelegramDMsMu.Unlock()
	if !ok {
		return
	}
	pending.cancel()
	go func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.telegramClient.DeleteMessage(deleteCtx, pending.chatID, pending.messageID); err != nil {
			slog.Error("delete magic link telegram message after sign-in", "error", err)
		}
	}()
}

func (h *Handler) ConsumeMagicLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}

	userID, err := h.users.ConsumeMagicLinkToken(r.Context(), auth.HashToken(body.Token))
	if errors.Is(err, users.ErrInvalidToken) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired sign-in link")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify sign-in link")
		return
	}
	h.issueSessionAndRespond(w, r, userID)
}

// ConsumeMagicLinkCode is the code counterpart to ConsumeMagicLink, for
// reading the email on one device and signing in on another — email and
// phone identify the person exactly as requesting a link does (the code
// alone is too low-entropy to look someone up by), and the response is
// the same generic 401 whether the code was wrong, expired, already
// used, guessed too many times, or the email/phone didn't match anyone
// at all: nothing here should tell those cases apart from the outside.
func (h *Handler) ConsumeMagicLinkCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Phone) == "" || strings.TrimSpace(body.Code) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "missing_fields", "email, phone, and code are all required")
		return
	}

	user, err := h.users.FindActiveByEmailAndPhone(r.Context(), body.Email, body.Phone)
	if err != nil {
		if !errors.Is(err, users.ErrNotFound) {
			slog.Error("consume magic link code: lookup failed", "error", err)
		}
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	userID, err := h.users.ConsumeMagicLinkCode(r.Context(), user.ID, auth.HashToken(body.Code))
	if errors.Is(err, users.ErrInvalidToken) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify code")
		return
	}
	h.issueSessionAndRespond(w, r, userID)
}

func (h *Handler) issueSessionAndRespond(w http.ResponseWriter, r *http.Request, userID int64) {
	sessionToken, err := h.tokens.Issue(r.Context(), userID, h.opts.TokenTTL)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	ip := httpapi.ClientIP(r)
	_ = h.users.TouchLastLogin(r.Context(), userID, ip)
	h.notifyLogin(r.Context(), userID, ip)
	h.deletePendingTelegramDM(userID)
	auth.SetSessionCookie(w, sessionToken, h.opts.PublicBaseURL, h.opts.TokenTTL)
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": sessionToken})
}

// notifyLogin pings Telegram about a successful sign-in. The login
// lookup runs synchronously on the request's own context, alongside
// every other database call this handler makes — a background goroutine
// touching the database independently of the request isn't safe against
// a request-scoped transaction (every handler test shares one). Only the
// network call to Telegram itself, the actually slow and unreliable
// part, runs on its own goroutine with a bounded timeout and its own
// background context, exactly like telegram.LogHandler.
func (h *Handler) notifyLogin(ctx context.Context, userID int64, ip string) {
	login, err := h.users.LoginForUserID(ctx, userID)
	if err != nil {
		login = fmt.Sprintf("user #%d", userID)
	}
	text := fmt.Sprintf("🔑 Login: %s (%s)", login, ip)
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.notifier.Send(sendCtx, text); err != nil {
			slog.Error("notify login to telegram", "error", err)
		}
	}()
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	_ = h.tokens.RevokeByHash(r.Context(), body.Token)
	auth.ClearSessionCookie(w, h.opts.PublicBaseURL)
	httpapi.WriteJSON(w, http.StatusNoContent, nil)
}

// Me reports the identity and roles of whoever the request's bearer
// token belongs to, resolved by the very auth middleware that let the
// request through — so it costs no extra database round trip. The
// frontend calls this once per page load to know who's signed in and
// what to show them, instead of decoding anything out of the token
// itself (there's nothing to decode: it's opaque).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	login, err := h.users.LoginForUserID(r.Context(), user.ID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve identity")
		return
	}
	response := map[string]any{"login": login, "roles": user.Roles}
	if user.ImpersonatedBy != nil {
		response["impersonated_by"] = h.impersonatorSummary(r.Context(), *user.ImpersonatedBy)
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

// impersonatorSummary is best-effort: it still gives the frontend banner
// something to show even if, say, the admin's own account was since
// deleted (an empty object) — never a numeric id.
func (h *Handler) impersonatorSummary(ctx context.Context, impersonatorID int64) map[string]any {
	impersonator, err := h.users.Get(ctx, impersonatorID)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"login": impersonator.Login, "first_name": impersonator.FirstName, "last_name": impersonator.LastName}
}

// ExitImpersonation ends the current impersonation session and hands
// back a fresh, normal token for the admin who started it — see
// tokens.Service.ExitImpersonation for why it's a new token rather than
// the one they had before.
func (h *Handler) ExitImpersonation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	if user.ImpersonatedBy == nil {
		httpapi.WriteError(w, http.StatusConflict, "not_impersonating", "this session isn't an impersonation")
		return
	}
	rawToken, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || rawToken == "" {
		httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return
	}

	newToken, err := h.tokens.ExitImpersonation(r.Context(), rawToken, *user.ImpersonatedBy, h.opts.TokenTTL)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not exit impersonation")
		return
	}
	auth.SetSessionCookie(w, newToken, h.opts.PublicBaseURL, h.opts.TokenTTL)
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": newToken})
}

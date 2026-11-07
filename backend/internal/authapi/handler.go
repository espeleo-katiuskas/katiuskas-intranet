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
	"math"
	"net"
	"net/http"
	"net/url"
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
	TokenTTL time.Duration
	// LocalTokenTTL is the (shorter) lifetime of a session opened through an address whose
	// first label is "local" (see sessionTTL); zero means no such thing, and every session
	// lasts TokenTTL.
	LocalTokenTTL        time.Duration
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

	// background tracks the magic-link DMs that run in their own goroutine (the Telegram DM riding
	// along a web request), so WaitForBackgroundSends can wait for them. The other sends of this
	// package are tracked by httpapi.Go.
	background sync.WaitGroup
}

// WaitForBackgroundSends blocks until every Telegram DM sent in the
// background by a web request has finished, together with what it does
// after a failure (forgetting an unreachable chat, see
// notifyMagicLinkViaTelegram). Nothing in a request needs it; it exists so
// a test can look at the outcome without racing the goroutine, and so a
// shutdown lets them finish (cmd/api).
func (h *Handler) WaitForBackgroundSends() {
	h.background.Wait()
}

// pendingTelegramDM is the latest magic-link DM (see
// notifyMagicLinkViaTelegram) sent to one particular user and not deleted
// yet — only the latest is ever kept in the chat: sending a new one
// deletes the one before it. Tracked so issueSessionAndRespond can delete
// it once that person actually signs in, and waiting out a scheduled
// expiry cleanup; cancel stops that cleanup's own goroutine from trying to
// delete the same message afterward.
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

// RequestMagicLink says so when email+phone match nobody who can sign in
// (404 unknown_details: no one has that pair, or the person holds no role that
// could use the API, see auth.BaselineRoles), so that a member who mistyped
// something knows to check it, or to ask the board. That lets the endpoint be
// used to check who's in the system, which the club accepts: it is public
// information within the club and the answer helps people far more than it
// exposes them; the per-address rate limit (ipLimiter) is what keeps it from
// being scanned. For a match it answers 200 whether or not a link was actually
// sent (a recent one might already be on its way, the cooldown), so the
// endpoint can't be used to spam a real person's inbox.
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

	outcome, user, err := h.sendMagicLinkIfEligible(r.Context(), body.Email, body.Phone, h.linkBase(r))
	if err != nil {
		slog.Error("magic link request failed", "error", err)
	}
	// An admin should be able to see that someone is typing details that
	// don't work — a typo, or a person who really should have access.
	switch outcome {
	case outcomeUnknownDetails:
		h.notifyAccessProblem(fmt.Sprintf("⚠️ Acceso fallido: ningún usuario coincide con el correo «%s» y el teléfono «%s» (%s).",
			logSafe(body.Email), logSafe(body.Phone), httpapi.ClientIP(r)))
	case outcomeNoAccess:
		h.notifyAccessProblem(fmt.Sprintf("⚠️ Acceso fallido: %s (%s) ha pedido un enlace, pero no tiene ningún rol que dé acceso (admin, board o member).",
			user.Login, httpapi.ClientIP(r)))
	}
	if outcome == outcomeUnknownDetails || outcome == outcomeNoAccess {
		// The same answer for both: that no one with access has that email and phone together.
		httpapi.WriteError(w, http.StatusNotFound, "unknown_details",
			"no one with access has that email and phone together")
		return
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

func (h *Handler) sendMagicLinkIfEligible(ctx context.Context, email, phone, linkBase string) (outcome magicLinkOutcome, user users.User, err error) {
	user, err = h.users.FindActiveByEmailAndPhone(ctx, email, phone)
	if errors.Is(err, users.ErrNotFound) {
		return outcomeUnknownDetails, user, nil
	}
	if err != nil {
		return outcomeIssued, user, err
	}
	roleNames, err := h.roles.ListNamesForUser(ctx, user.ID)
	if err != nil {
		return outcomeIssued, user, err
	}
	if !auth.HasBaselineRole(roleNames) {
		return outcomeNoAccess, user, nil
	}
	return outcomeIssued, user, h.issueMagicLink(ctx, user, linkBase, false)
}

// magicLinkOutcome says how a web request for a link went: what is logged
// about it, and whether the caller is told that the details match nobody.
type magicLinkOutcome int

const (
	// outcomeIssued: the details matched someone with access (a link goes
	// out, unless the cooldown holds it back).
	outcomeIssued magicLinkOutcome = iota
	// outcomeUnknownDetails: no one has that email and phone together.
	outcomeUnknownDetails
	// outcomeNoAccess: the details match someone with no role that gives
	// access to anything, so no link is sent.
	outcomeNoAccess
)

// logSafe makes something a stranger typed fit on one line of a log
// message, and short.
func logSafe(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 100 {
		return string(r[:100]) + "…"
	}
	return s
}

// notifyAccessProblem sends a line to the log channel, like notifyLogin
// does for a successful sign-in, in the background so the request never
// waits on Telegram.
func (h *Handler) notifyAccessProblem(text string) {
	httpapi.Go(func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.notifier.Send(sendCtx, text); err != nil {
			slog.Error("notify access problem to telegram", "error", err)
		}
	})
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
	return h.issueMagicLink(ctx, user, h.opts.PublicBaseURL, true)
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
func (h *Handler) issueMagicLink(ctx context.Context, user users.User, linkBase string, viaTelegramBot bool) error {
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

	link := fmt.Sprintf("%s/login?token=%s", linkBase, rawToken)
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
// first. Neither is worth keeping once the link and code in it stop
// working, so each is deleted when that moment comes — the bot-flow DM
// after TelegramBotDMLinkTTL, the one that rode along a web request after
// MagicLinkTTL — or earlier, as soon as the person actually signs in (see
// deletePendingTelegramDM): whichever way, link or code, and from
// whichever device. And only the latest is worth having at all: sending a
// new one deletes the previous one (see setPendingTelegramDM).
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
		messageID, err := h.telegramClient.SendMessageForID(sendCtx, *chatID, text)
		if telegram.IsUnreachable(err) {
			// The bot can't talk to this person any more (they closed their
			// account, or the chat is gone for good): the stored chat id is
			// useless, and keeping it would only fail again at every
			// sign-in — and make them look like someone reachable by
			// Telegram. They can link again by sharing their contact.
			if clearErr := h.users.ClearTelegramChatID(sendCtx, userID); clearErr != nil {
				slog.Error("forget unreachable telegram chat", "error", clearErr)
			} else {
				slog.Info("forgot an unreachable telegram chat", "user_id", userID, "reason", err)
			}
		}
		return messageID, err
	}
	// The link itself stops working on its own once it expires, so a
	// message still holding it is only misleading — and one more thing
	// exposed for no reason if the phone/session is compromised later.
	// This deletion is purely a hygiene nicety, not something the link's
	// own security depends on: it's best-effort (a process restart in
	// between loses it, like any other cleanup here), and a failure is
	// only logged. The delay is the validity the message itself states:
	// h.opts.TelegramBotDMLinkTTL for a bot-flow DM (shorter than the real
	// expiry, see Options), h.opts.MagicLinkTTL for one that rode along a
	// web request — unless issueSessionAndRespond (see
	// deletePendingTelegramDM) fires this early, once the person this DM
	// was sent to has actually signed in.
	scheduleCleanup := func(messageID int64, delay time.Duration) {
		cleanupCtx, cancel := context.WithCancel(context.Background())
		h.deleteTelegramDMs(h.setPendingTelegramDM(userID, *chatID, messageID, cancel))
		go func() {
			select {
			case <-time.After(delay):
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
		scheduleCleanup(messageID, h.opts.TelegramBotDMLinkTTL)
		return nil
	}

	h.background.Add(1)
	go func() {
		defer h.background.Done()
		messageID, err := send()
		if err != nil {
			slog.Error("send magic link via telegram", "error", err)
			return
		}
		scheduleCleanup(messageID, h.opts.MagicLinkTTL)
	}()
	return nil
}

// setPendingTelegramDM records the DM just sent to userID as the one to
// keep, and returns what is to be deleted now (see deleteTelegramDMs): the
// DM it replaces. Normally that is the previous one; but two requests in
// quick succession can have their background sends finish out of order,
// and then it is the one just sent that is the older — Telegram's message
// ids grow within a chat — so that one is returned instead and the newer
// stays.
func (h *Handler) setPendingTelegramDM(userID, chatID, messageID int64, cancel context.CancelFunc) []pendingTelegramDM {
	h.pendingTelegramDMsMu.Lock()
	defer h.pendingTelegramDMsMu.Unlock()
	fresh := pendingTelegramDM{chatID: chatID, messageID: messageID, cancel: cancel}
	previous, ok := h.pendingTelegramDMs[userID]
	if !ok {
		h.pendingTelegramDMs[userID] = fresh
		return nil
	}
	if previous.chatID == chatID && previous.messageID > messageID {
		return []pendingTelegramDM{fresh}
	}
	h.pendingTelegramDMs[userID] = fresh
	return []pendingTelegramDM{previous}
}

// clearPendingTelegramDM drops the tracked entry once its own scheduled
// cleanup has actually run — only if it is still that message, not a newer
// one that has since replaced it.
func (h *Handler) clearPendingTelegramDM(userID, messageID int64) {
	h.pendingTelegramDMsMu.Lock()
	defer h.pendingTelegramDMsMu.Unlock()
	if pending, ok := h.pendingTelegramDMs[userID]; ok && pending.messageID == messageID {
		delete(h.pendingTelegramDMs, userID)
	}
}

// deleteTelegramDMs deletes the given DMs from their chats in the
// background, first cancelling each one's scheduled expiry cleanup so
// that goroutine doesn't also try to delete the same, already-gone
// message. A failure is only logged: this is hygiene, not security.
func (h *Handler) deleteTelegramDMs(list []pendingTelegramDM) {
	if len(list) == 0 {
		return
	}
	for _, pending := range list {
		if pending.cancel != nil {
			pending.cancel()
		}
	}
	httpapi.Go(func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, pending := range list {
			if err := h.telegramClient.DeleteMessage(deleteCtx, pending.chatID, pending.messageID); err != nil {
				slog.Error("delete magic link telegram message", "error", err)
			}
		}
	})
}

// deletePendingTelegramDM immediately deletes the magic-link DM (see
// notifyMagicLinkViaTelegram) still pending for userID, bot-flow or
// web-request, cancelling its scheduled expiry cleanup. Called once a
// sign-in actually succeeds (see issueSessionAndRespond), whichever way
// it happened, so the message disappears from the chat right away. A no-op
// for anyone with no linked Telegram, or no DM sent, since there's nothing
// tracked.
func (h *Handler) deletePendingTelegramDM(userID int64) {
	h.pendingTelegramDMsMu.Lock()
	pending, ok := h.pendingTelegramDMs[userID]
	delete(h.pendingTelegramDMs, userID)
	h.pendingTelegramDMsMu.Unlock()
	if ok {
		h.deleteTelegramDMs([]pendingTelegramDM{pending})
	}
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
		if errors.Is(err, users.ErrNotFound) {
			h.notifyAccessProblem(fmt.Sprintf("⚠️ Código de acceso rechazado: ningún usuario coincide con el correo «%s» y el teléfono «%s» (%s).",
				logSafe(body.Email), logSafe(body.Phone), httpapi.ClientIP(r)))
		} else {
			slog.Error("consume magic link code: lookup failed", "error", err)
		}
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	userID, err := h.users.ConsumeMagicLinkCode(r.Context(), user.ID, auth.HashToken(body.Code))
	if errors.Is(err, users.ErrInvalidToken) {
		h.notifyAccessProblem(fmt.Sprintf("⚠️ Código de acceso rechazado: el código de %s es incorrecto o ha caducado (%s).",
			user.Login, httpapi.ClientIP(r)))
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid or expired code")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify code")
		return
	}
	h.issueSessionAndRespond(w, r, userID)
}

// localHostLabel names the addresses whose sessions are short: a request whose host starts with
// "local" — local.intranet.katiuskas.es, the address the club's shared tablet is given — gets a
// session of LocalTokenTTL instead of TokenTTL. Which address someone used is nothing to trust (a
// client can send any Host), and nothing needs it to be: it can only shorten a session.
const localHostLabel = "local"

// requestHost is the address the client used, without its port and in lower case: the
// X-Forwarded-Host the reverse proxy adds, if there is one, else the Host header.
func requestHost(r *http.Request) string {
	host, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ",")
	host = strings.TrimSpace(host)
	if host == "" {
		host = r.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

// isLocalHost says whether the first component of host, up to the first ".", is "local".
func isLocalHost(host string) bool {
	first, _, _ := strings.Cut(host, ".")
	return first == localHostLabel
}

// sessionTTL is how long the session of a sign-in lasts: TokenTTL, or the shorter LocalTokenTTL
// when the request was made through a "local" address. Nothing is remembered about a device and a
// request cannot ask for a longer session: what decides is the address used.
func (h *Handler) sessionTTL(r *http.Request) time.Duration {
	if isLocalHost(requestHost(r)) && h.opts.LocalTokenTTL > 0 && h.opts.LocalTokenTTL < h.opts.TokenTTL {
		return h.opts.LocalTokenTTL
	}
	return h.opts.TokenTTL
}

// linkBase is where the link in a sign-in email points. Normally PublicBaseURL; but for a request
// made through local.<that same address> it points there too, so that opening the link on the
// device that asked (the tablet) finishes the sign-in at the local address and gets the short
// session — a link to the main address would give a 30-day one. Only that exact address, built
// from PublicBaseURL, is accepted: a Host header anybody could forge must never decide where a
// link in an email to somebody else points.
func (h *Handler) linkBase(r *http.Request) string {
	u, err := url.Parse(h.opts.PublicBaseURL)
	if err != nil || u.Hostname() == "" {
		return h.opts.PublicBaseURL
	}
	if requestHost(r) == localHostLabel+"."+strings.ToLower(u.Hostname()) {
		u.Host = localHostLabel + "." + u.Host
		return u.String()
	}
	return h.opts.PublicBaseURL
}

func (h *Handler) issueSessionAndRespond(w http.ResponseWriter, r *http.Request, userID int64) {
	ttl := h.sessionTTL(r)
	sessionToken, err := h.tokens.Issue(r.Context(), userID, ttl)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not issue session")
		return
	}
	ip := httpapi.ClientIP(r)
	_ = h.users.TouchLastLogin(r.Context(), userID, ip)
	h.notifyLogin(r.Context(), userID, ip)
	h.deletePendingTelegramDM(userID)
	auth.SetSessionCookie(w, sessionToken, h.opts.PublicBaseURL, ttl)
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
	httpapi.Go(func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.notifier.Send(sendCtx, text); err != nil {
			slog.Error("notify login to telegram", "error", err)
		}
	})
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
	who, err := h.users.IdentityForUserID(r.Context(), user.ID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not resolve identity")
		return
	}
	// The name rides along so a page can greet the person (and, when
	// impersonating, say who they are acting as) without another request.
	// A session now expires a while after its last use, so the cookie that mirrors it (for the
	// file links, see auth.SetSessionCookie) is renewed with it: set again here, once per page
	// load, for as long as the session has left. Without it the cookie would die at the time the
	// session was opened plus its lifetime, however much it was used.
	secondsLeft := -1
	if raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); raw != "" {
		if expires, err := h.tokens.ExpiresAtForRawToken(r.Context(), raw); err == nil {
			auth.SetSessionCookie(w, raw, h.opts.PublicBaseURL, time.Until(expires))
			secondsLeft = int(math.Ceil(time.Until(expires).Seconds()))
		}
	}
	response := map[string]any{"login": who.Login, "first_name": who.FirstName, "last_name": who.LastName, "roles": user.Roles}
	if user.ImpersonatedBy != nil {
		response["impersonated_by"] = h.impersonatorSummary(r.Context(), *user.ImpersonatedBy)
		// How long the impersonation has left, as a duration and not as a moment: the page counts it
		// down from when it got this answer, so a browser clock that is off cannot make it reload
		// early (or over and over). An impersonation session has a fixed expiry, so this is when it ends.
		if secondsLeft >= 0 {
			response["impersonation_seconds_left"] = secondsLeft
		}
	}
	// An admin acting with other roles (see tokens.Handler's role-override routes): the roles above
	// are the ones in force, these say what they really are and how long the change lasts, as a
	// duration for the same reason as impersonation_seconds_left.
	if user.RoleOverrideUntil != nil {
		response["real_roles"] = user.RealRoles
		response["role_override_seconds_left"] = int(math.Ceil(time.Until(*user.RoleOverrideUntil).Seconds()))
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

	newToken, err := h.tokens.ExitImpersonation(r.Context(), rawToken, *user.ImpersonatedBy, h.sessionTTL(r))
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not exit impersonation")
		return
	}
	auth.SetSessionCookie(w, newToken, h.opts.PublicBaseURL, h.sessionTTL(r))
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": newToken})
}

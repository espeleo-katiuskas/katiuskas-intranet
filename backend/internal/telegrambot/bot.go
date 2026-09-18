// Package telegrambot lets the club's Telegram bot react to messages
// from people, not just send logs and notifications the other way — see
// internal/telegram for the low-level client this is built on, and
// docs/architecture.md for how this fits alongside the other five
// notifications (error logs, logins, magic-link DMs, mutating API
// calls, and the startup ping).
//
// It does two things. First, turn "someone we don't yet recognize
// messages the bot" into a linked Telegram account, by asking them to
// share their contact and matching its phone number against
// users.phone. Every attempt to link an account — matched or not — is
// also reported to the Telegram log channel, same as every other
// notification in this app. Second, once a chat is recognized, it can
// ask for a sign-in link — but only after proving it's still actually
// that person by typing their own registered email, not just by being
// whoever's holding the phone with Telegram already open on it; see
// handleRecognizedChat. Being linked at all doesn't automatically mean
// being allowed to sign in, though — canUseTelegramLogin gates both of
// the above on the same baseline-role check every route in this API
// enforces, plus having an email and a phone on file; someone who
// fails it never gets asked for their email at all, just told the bot
// isn't for them.
package telegrambot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

// greeting asks for a contact share and, since the button that does it
// lives in Telegram's own custom-keyboard panel rather than inline in
// the chat, spells out how to get to it — SendMessageRequestingContact
// already asks the client to show that panel open by default
// (is_persistent), but a client that doesn't honor it would otherwise
// leave the button hidden behind an icon next to the message box with
// no clue that tapping it is what reveals anything at all.
const greeting = "¡Hola! Soy el bot del Club de Espeleología Katiuskas. " +
	"Si eres socio o socia del club, compárteme tu contacto para identificarte: " +
	"toca el botón «" + contactButtonText + "» que aparece debajo " +
	"(si no lo ves, pulsa primero el icono de teclado que hay a la derecha)."

const contactButtonText = "Compartir mi contacto"

// emailPrompt is what an already-recognized chat gets in reply to any
// message that isn't its own registered email — see handleRecognizedChat.
const emailPrompt = "Si quieres entrar en la intranet, envíame tu dirección de correo electrónico."

// nonTextEmailReply is emailAttemptReply's answer to a message that
// isn't text at all — a photo, sticker, voice note... Telegram never
// sends a real text message with an empty body, so an empty Text always
// means something that isn't text.
const nonTextEmailReply = "No admito ese tipo de mensajes — tienes que enviarme solo texto, con tu dirección de correo electrónico."

// onlyEmailReply is emailAttemptReply's answer to a message that
// carries more than one word — the address on its own is what's
// expected, not a sentence that happens to contain one.
const onlyEmailReply = "Envíame solo tu dirección de correo electrónico, y nada más."

// notAnEmailReplyFormat is emailAttemptReply's answer to a single word
// that doesn't even look like an email address (no "@"). %s is that
// word, repeated back so there's no doubt about what was rejected.
const notAnEmailReplyFormat = "«%s» no es una dirección de correo electrónico. Envíame una dirección válida, como usuario@gmail.com."

// unknownEmailReplyFormat is emailAttemptReply's answer to a single
// word that does look like an email address but isn't one of this
// account's own registered ones. %s is that address, repeated back so
// the person can spot a typo themselves.
const unknownEmailReplyFormat = "No tenemos apuntada la dirección «%s». Si crees que es un error, ponte en contacto con el club."

// emailAttemptReply figures out what handleRecognizedChat should tell
// an already-linked chat about their latest message, on the way to
// matching one of user's own registered emails. Returns ("", true)
// only for a genuine match; every other case — an attachment instead
// of text, more than one word, something not shaped like an email at
// all, or an email that just isn't this account's — gets its own
// specific, actionable reply instead of the same generic emailPrompt
// regardless of what actually went wrong.
func emailAttemptReply(user users.User, text string) (reply string, matched bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nonTextEmailReply, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) > 1 {
		return onlyEmailReply, false
	}
	candidate := fields[0]
	if !strings.Contains(candidate, "@") {
		return fmt.Sprintf(notAnEmailReplyFormat, candidate), false
	}
	if emailMatchesUser(user, candidate) {
		return "", true
	}
	return fmt.Sprintf(unknownEmailReplyFormat, candidate), false
}

// contactClubHint is appended to any reply telling someone the bot
// can't help them right now — an unrecognized phone number, or being
// linked but not actually eligible to sign in — so being told no
// always comes with a next step instead of a dead end.
const contactClubHint = "Si crees que deberías tener acceso, ponte en contacto con el club para comprobar tu situación."

// unauthorizedMessage replaces emailPrompt for a chat linked to someone
// who isn't actually eligible to sign in at all — see
// canUseTelegramLogin. Sent instead of ever asking for their email,
// since asking would just be a dead end for them.
const unauthorizedMessage = "Este bot está disponible únicamente para usuarios autorizados. " + contactClubHint

// magicLinkDeliveryFailedMessage answers a matched email when
// magicLinkIssuer.IssueMagicLinkForTelegramUser itself fails (a
// transient error sending the DM, most likely, rather than anything
// about the person asking) — this reply *is* the only channel this
// request has (see authapi.Handler.issueMagicLink's own
// viaTelegramBot branch, which skips the email entirely here), so
// silence on failure would leave whoever just proved their email with
// no link and no idea anything went wrong at all.
const magicLinkDeliveryFailedMessage = "No se ha podido generar tu enlace de acceso en este momento. " +
	"Inténtalo de nuevo en unos minutos; si el problema persiste, ponte en contacto con el club."

// canUseTelegramLogin reports whether user could sign in at all: the
// same baseline-role gate every route in this API enforces
// (auth.HasBaselineRole — in practice, an active socio via the
// automatic "member" role, or admin/board), plus having both an email
// and a phone on file. The web magic-link request already requires
// both as input to even resolve a match (FindActiveByEmailAndPhone),
// but a Telegram-linked account is resolved straight from its chat id
// instead, so nothing there guarantees either is actually on file —
// issueMagicLink already tolerates a missing email for that reason
// (see authapi.Handler), but a magic link genuinely needs both to be
// useful for the person receiving it.
func canUseTelegramLogin(user users.User) bool {
	return auth.HasBaselineRole(user.Roles) && user.Email != nil && user.Phone != nil
}

// unauthorizedAfterLinkReplyFormat is handleContact's own version of
// unauthorizedMessage: a single combined reply for someone whose
// contact just matched a real socio but who isn't actually eligible to
// sign in, instead of "you're linked" followed by "but you can't use
// this" as two separate messages with nothing useful in between. %s %s
// is their first and last name.
const unauthorizedAfterLinkReplyFormat = "¡Gracias! Te hemos identificado como %s %s. Sin embargo, " +
	"este bot está disponible únicamente para usuarios autorizados. " + contactClubHint

// pollTimeoutSeconds is how long each GetUpdates call blocks server-side
// waiting for a new message before returning empty — long enough to
// avoid hammering Telegram with near-immediate re-polls, short enough
// that Run notices ctx being cancelled in reasonable time between calls.
const pollTimeoutSeconds = 30

// getUpdatesFailureLogThreshold is how many consecutive GetUpdates
// failures it takes for Run to treat one as a real, ongoing problem
// (see logGetUpdatesFailure) rather than a one-off blip — Telegram's
// own long-poll connection dropping or timing out for a moment, almost
// always resolved by the very next attempt. Was 2 until a run of
// exactly 2 consecutive failures turned out to still be common enough
// in practice (roughly once a week) to be its own kind of blip, not a
// real problem worth surfacing at ERROR.
const getUpdatesFailureLogThreshold = 3

// botClient is the subset of *telegram.Client this package needs — a
// local interface, the same pattern other packages use for UserLookup,
// so tests can inject a fake instead of making real HTTP calls.
type botClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	SendMessageRequestingContact(ctx context.Context, chatID int64, text, buttonText string) error
	SendMessageRemovingKeyboard(ctx context.Context, chatID int64, text string) error
	DeleteMessage(ctx context.Context, chatID, messageID int64) error
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]telegram.Update, error)
}

// magicLinkIssuer lets an already-linked chat get a fresh sign-in link
// just by messaging the bot, exactly as if they'd requested one from
// the web — *authapi.Handler satisfies this structurally, the same
// narrow-dependency pattern as botClient above.
type magicLinkIssuer interface {
	IssueMagicLinkForTelegramUser(ctx context.Context, userID int64) error
}

// Bot polls Telegram for messages and reacts to them — see Run.
type Bot struct {
	client      botClient
	users       *users.Service
	logNotifier telegram.Sender
	magicLinks  magicLinkIssuer
}

func New(client *telegram.Client, usersService *users.Service, logNotifier telegram.Sender, magicLinks magicLinkIssuer) *Bot {
	return &Bot{client: client, users: usersService, logNotifier: logNotifier, magicLinks: magicLinks}
}

// shouldEscalateGetUpdatesFailure reports whether the nth consecutive
// GetUpdates failure in a row should be logged at ERROR instead of
// WARN — true only the one time a run of failures first reaches
// getUpdatesFailureLogThreshold, not on every one after that, so a
// longer outage doesn't re-spam the log channel on every retry once
// it's already been flagged once.
func shouldEscalateGetUpdatesFailure(consecutiveFailures int) bool {
	return consecutiveFailures == getUpdatesFailureLogThreshold
}

// logGetUpdatesFailure logs one failed GetUpdates call at a level
// matching how many times in a row it's now happened. Below
// getUpdatesFailureLogThreshold, WARN: visible in `docker compose logs
// api` for anyone actively looking, but below telegram.LogHandler's own
// forwarding threshold (see cmd/api/main.go), so an isolated blip never
// reaches the log channel at all. At exactly the threshold, ERROR —
// this stopped looking like a blip and started looking like a real
// problem, so it's worth surfacing there too. Past the threshold, back
// to WARN, so an outage that drags on doesn't keep re-notifying the
// channel every 5 seconds for as long as it lasts.
func logGetUpdatesFailure(err error, consecutiveFailures int) {
	if shouldEscalateGetUpdatesFailure(consecutiveFailures) {
		slog.Error("telegram bot: get updates", "error", err, "consecutive_failures", consecutiveFailures)
		return
	}
	slog.Warn("telegram bot: get updates", "error", err, "consecutive_failures", consecutiveFailures)
}

// Run polls Telegram for new messages until ctx is cancelled. Meant to
// be started with `go bot.Run(ctx)`; it blocks the calling goroutine and
// never returns on its own otherwise.
func (b *Bot) Run(ctx context.Context) {
	// Skips whatever accumulated while the bot wasn't running, rather
	// than replaying it — the offset only lives in memory, so without
	// this every restart (a routine deploy, see docs/deployment.md)
	// would re-process the same backlog Telegram is still holding onto
	// (up to 24h of it) and, e.g., re-notify the channel about a contact
	// share that already went through. Missing a message sent in the
	// gap while the process was down is the accepted trade-off — this
	// bot only needs to react to what's happening now.
	offset, err := b.latestUpdateOffset(ctx)
	if err != nil {
		slog.Error("telegram bot: get initial offset", "error", err)
	}
	// How many GetUpdates calls in a row have just failed — reset to 0
	// on the next successful one. See logGetUpdatesFailure: a single
	// failure is almost always Telegram's own long-poll connection
	// dropping or timing out for a moment, not a real, ongoing problem,
	// so it's logged at a level this process's own
	// telegram.LogHandler (see cmd/api/main.go) doesn't forward to the
	// log channel — only once failures actually start repeating does
	// this stop looking like a blip and get escalated.
	consecutiveFailures := 0
	for ctx.Err() == nil {
		updates, err := b.client.GetUpdates(ctx, offset, pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveFailures++
			logGetUpdatesFailure(err, consecutiveFailures)
			time.Sleep(5 * time.Second)
			continue
		}
		consecutiveFailures = 0
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handle(ctx, u)
		}
	}
}

// latestUpdateOffset returns the offset that skips every update already
// sitting in Telegram's queue, without acting on any of them — see Run.
func (b *Bot) latestUpdateOffset(ctx context.Context) (int64, error) {
	updates, err := b.client.GetUpdates(ctx, 0, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}
	return updates[len(updates)-1].UpdateID + 1, nil
}

func (b *Bot) handle(ctx context.Context, u telegram.Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	if msg.Contact != nil {
		b.handleContact(ctx, msg)
		return
	}

	userID, err := b.users.UserIDForTelegramChatID(ctx, msg.Chat.ID)
	if errors.Is(err, users.ErrNotFound) {
		// Not recognized yet: greet and ask for their contact.
		if err := b.client.SendMessageRequestingContact(ctx, msg.Chat.ID, greeting, contactButtonText); err != nil {
			slog.Error("telegram bot: send greeting", "error", err)
		}
		return
	}
	if err != nil {
		slog.Error("telegram bot: look up linked chat", "error", err)
		return
	}
	b.handleRecognizedChat(ctx, msg, userID)
}

// handleRecognizedChat is the second factor an already-linked chat has
// to clear before getting a sign-in link: merely being whoever's
// holding a socio's phone with Telegram already open — a borrowed
// phone, an unlocked session left unattended — used to be enough, since
// any message at all triggered one. That's a real step down from
// requesting one on the web, which needs both phone *and* email. Now
// the bot instead asks for the account's own registered email, and only
// issues the link once it gets it — restoring "you also have to know
// something, not just have the device" the same way the web flow
// already does.
func (b *Bot) handleRecognizedChat(ctx context.Context, msg *telegram.Message, userID int64) {
	user, err := b.users.Get(ctx, userID)
	if err != nil {
		slog.Error("telegram bot: look up linked user", "error", err)
		return
	}
	if !canUseTelegramLogin(user) {
		if err := b.client.SendMessage(ctx, msg.Chat.ID, unauthorizedMessage); err != nil {
			slog.Error("telegram bot: send unauthorized message", "error", err)
		}
		return
	}
	reply, matched := emailAttemptReply(user, msg.Text)
	if !matched {
		if err := b.client.SendMessage(ctx, msg.Chat.ID, reply); err != nil {
			slog.Error("telegram bot: send email prompt", "error", err)
		}
		return
	}

	// The message just proved it — delete it so the email doesn't sit
	// around in the chat history for whoever might get hold of this
	// phone/session afterward. Best-effort: deleting can fail (an old
	// message, revoked rights, ...) without that being any reason to
	// withhold the sign-in link itself.
	if err := b.client.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID); err != nil {
		slog.Error("telegram bot: delete email message", "error", err)
	}
	if err := b.magicLinks.IssueMagicLinkForTelegramUser(ctx, userID); err != nil {
		slog.Error("telegram bot: issue magic link", "error", err)
		// This DM is the only channel this request ever gets (see
		// authapi.Handler.issueMagicLink's own viaTelegramBot branch) —
		// without this reply, whoever just proved their email would be
		// left with neither the link nor any sign that anything had
		// gone wrong at all.
		if err := b.client.SendMessage(ctx, msg.Chat.ID, magicLinkDeliveryFailedMessage); err != nil {
			slog.Error("telegram bot: send magic link failure notice", "error", err)
		}
	}
}

// emailMatchesUser reports whether candidate is one of user's own
// registered emails — compared case-insensitively and trimmed (a typo
// in casing shouldn't matter here the way it might for, say, a phone
// digit), against the same space-separated multi-email convention
// users.Repository.FindActiveByEmailAndPhone already uses.
func emailMatchesUser(user users.User, candidate string) bool {
	if user.Email == nil {
		return false
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	for _, email := range strings.Fields(*user.Email) {
		if strings.EqualFold(email, candidate) {
			return true
		}
	}
	return false
}

var nonDigits = regexp.MustCompile(`\D`)

func (b *Bot) handleContact(ctx context.Context, msg *telegram.Message) {
	// Telegram lets someone attach *any* contact card to a message, not
	// just their own — a manually typed vCard, or a different Telegram
	// user's saved contact. Only the "share contact" button this bot
	// itself offers (SendMessageRequestingContact) guarantees
	// Contact.UserID equals the sender's own From.ID; anything else
	// means whoever's sharing this isn't necessarily who the phone
	// number belongs to. Without this check, anyone who knew — or made
	// up — a socio's phone number could link that socio's account to
	// their *own* Telegram chat, and from then on receive that socio's
	// magic-link/login code themselves.
	if !isOwnContact(msg) {
		reply := "Por tu seguridad, solo puedo vincular tu propio contacto — compártelo con el botón, no el de otra persona."
		if err := b.client.SendMessage(ctx, msg.Chat.ID, reply); err != nil {
			slog.Error("telegram bot: send reply", "error", err)
		}
		b.notifyChannelRejectedForeignContact(ctx, msg)
		return
	}

	digits := nonDigits.ReplaceAllString(msg.Contact.PhoneNumber, "")

	matched, err := b.users.FindByPhoneNumber(ctx, digits)
	switch {
	case err == nil:
		if linkErr := b.users.SetTelegramChatID(ctx, matched.ID, msg.Chat.ID); linkErr != nil {
			slog.Error("telegram bot: link telegram chat id", "error", linkErr)
			return
		}
		b.replyToLinkedContact(ctx, msg, matched)
		b.notifyChannel(ctx, msg, msg.Contact.PhoneNumber, &matched)
	case errors.Is(err, users.ErrNotFound):
		reply := "No hemos encontrado a nadie con ese número de teléfono en la base de datos del club. " + contactClubHint
		// Same reasoning as the match case above: this is still their
		// own contact, just not on file, so tapping the button again
		// would only resend the same number and get the same answer.
		if sendErr := b.client.SendMessageRemovingKeyboard(ctx, msg.Chat.ID, reply); sendErr != nil {
			slog.Error("telegram bot: send reply", "error", sendErr)
		}
		b.notifyChannel(ctx, msg, msg.Contact.PhoneNumber, nil)
	default:
		slog.Error("telegram bot: look up phone", "error", err)
	}
}

// replyToLinkedContact tells a chat what happened right after its
// shared contact matched a real socio (handleContact's own success
// case): the plain link confirmation followed by emailPrompt for
// someone actually eligible to sign in, or — when they're not — a
// single combined reply (unauthorizedAfterLinkReplyFormat) instead of
// splitting "you're linked" and "but you can't use this bot" into two
// separate messages with nothing useful in between. matched itself
// only carries the plain columns (FindByPhoneNumber doesn't enrich),
// so this needs its own Get for Roles to be populated before
// canUseTelegramLogin can say anything useful; a Get failure fails
// open to the plain confirmation alone, same as before eligibility
// gating existed here.
func (b *Bot) replyToLinkedContact(ctx context.Context, msg *telegram.Message, matched users.User) {
	enriched, err := b.users.Get(ctx, matched.ID)
	if err != nil {
		slog.Error("telegram bot: look up linked user", "error", err)
	}
	eligible := err == nil && canUseTelegramLogin(enriched)

	var reply string
	if err != nil || eligible {
		reply = fmt.Sprintf("¡Gracias! Te hemos identificado como %s %s y hemos vinculado tu cuenta de Telegram.",
			matched.FirstName, matched.LastName)
	} else {
		reply = fmt.Sprintf(unauthorizedAfterLinkReplyFormat, matched.FirstName, matched.LastName)
	}
	// The "share your contact" button has done its job — leaving it up
	// would only invite a pointless second tap that resends the same
	// phone number, so this reply always removes it.
	if sendErr := b.client.SendMessageRemovingKeyboard(ctx, msg.Chat.ID, reply); sendErr != nil {
		slog.Error("telegram bot: send reply", "error", sendErr)
	}
	if eligible {
		if sendErr := b.client.SendMessage(ctx, msg.Chat.ID, emailPrompt); sendErr != nil {
			slog.Error("telegram bot: send email prompt", "error", sendErr)
		}
	}
}

// isOwnContact reports whether the contact msg carries is the sender's
// own — see the security note in handleContact above for why this
// matters. Contact.UserID is 0 whenever Telegram doesn't know who the
// shared contact actually is (a manually typed vCard), which never
// equals a real From.ID either.
func isOwnContact(msg *telegram.Message) bool {
	return msg.From != nil && msg.Contact.UserID != 0 && msg.Contact.UserID == msg.From.ID
}

func senderName(msg *telegram.Message) string {
	if msg.From != nil && msg.From.FirstName != "" {
		return msg.From.FirstName
	}
	return "alguien"
}

// notifyChannel reports every contact-share attempt to the log channel,
// matched or not — the club wants visibility into who's trying to link
// an account, not just successful ones.
func (b *Bot) notifyChannel(ctx context.Context, msg *telegram.Message, phone string, matched *users.User) {
	var text string
	if matched != nil {
		text = fmt.Sprintf("📇 %s ha compartido su contacto (%s) por Telegram: coincide con %s.",
			senderName(msg), phone, matched.Login)
	} else {
		text = fmt.Sprintf("📇 %s ha compartido su contacto (%s) por Telegram: no coincide con ningún socio.",
			senderName(msg), phone)
	}
	if err := b.logNotifier.Send(ctx, text); err != nil {
		slog.Error("telegram bot: notify channel", "error", err)
	}
}

// notifyChannelRejectedForeignContact reports a rejected attempt to
// link an account via someone else's contact card (see the security
// note in handleContact) — this is worth the club knowing about even
// more than a routine no-match, since it's a possible impersonation
// attempt rather than just someone fumbling the bot.
func (b *Bot) notifyChannelRejectedForeignContact(ctx context.Context, msg *telegram.Message) {
	text := fmt.Sprintf("⚠️ %s ha intentado vincular un contacto que no es el suyo (%s) por Telegram: vinculación rechazada.",
		senderName(msg), msg.Contact.PhoneNumber)
	if err := b.logNotifier.Send(ctx, text); err != nil {
		slog.Error("telegram bot: notify channel", "error", err)
	}
}

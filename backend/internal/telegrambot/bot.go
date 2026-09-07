// Package telegrambot lets the club's Telegram bot react to messages
// from people, not just send logs and notifications the other way — see
// internal/telegram for the low-level client this is built on, and
// CLAUDE.md for how this fits alongside the other five notifications
// (error logs, logins, magic-link DMs, mutating API calls, and the
// startup ping).
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
// handleRecognizedChat.
package telegrambot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

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
	"toca el botón «" + contactButtonText + "» que aparece junto al campo donde escribes el mensaje " +
	"(si no lo ves, pulsa primero el icono de teclado ⌨️ que hay junto a ese campo)."

const contactButtonText = "Compartir mi contacto"

// emailPrompt is what an already-recognized chat gets in reply to any
// message that isn't its own registered email — see handleRecognizedChat.
const emailPrompt = "Si quieres entrar en la intranet, envíame tu dirección de correo electrónico."

// pollTimeoutSeconds is how long each GetUpdates call blocks server-side
// waiting for a new message before returning empty — long enough to
// avoid hammering Telegram with near-immediate re-polls, short enough
// that Run notices ctx being cancelled in reasonable time between calls.
const pollTimeoutSeconds = 30

// botClient is the subset of *telegram.Client this package needs — a
// local interface, the same pattern other packages use for UserLookup,
// so tests can inject a fake instead of making real HTTP calls.
type botClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	SendMessageRequestingContact(ctx context.Context, chatID int64, text, buttonText string) error
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
	for ctx.Err() == nil {
		updates, err := b.client.GetUpdates(ctx, offset, pollTimeoutSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("telegram bot: get updates", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}
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
	if !emailMatchesUser(user, msg.Text) {
		if err := b.client.SendMessage(ctx, msg.Chat.ID, emailPrompt); err != nil {
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
		reply := fmt.Sprintf("¡Gracias! Te hemos identificado como %s %s y hemos vinculado tu cuenta de Telegram.",
			matched.FirstName, matched.LastName)
		if sendErr := b.client.SendMessage(ctx, msg.Chat.ID, reply); sendErr != nil {
			slog.Error("telegram bot: send reply", "error", sendErr)
		}
		b.notifyChannel(ctx, msg, msg.Contact.PhoneNumber, &matched)
	case errors.Is(err, users.ErrNotFound):
		reply := "No hemos encontrado a nadie con ese número de teléfono en la base de datos del club."
		if sendErr := b.client.SendMessage(ctx, msg.Chat.ID, reply); sendErr != nil {
			slog.Error("telegram bot: send reply", "error", sendErr)
		}
		b.notifyChannel(ctx, msg, msg.Contact.PhoneNumber, nil)
	default:
		slog.Error("telegram bot: look up phone", "error", err)
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

// Package telegrambot lets the club's Telegram bot react to messages
// from people, not just send logs and notifications the other way — see
// internal/telegram for the low-level client this is built on, and
// CLAUDE.md for how this fits alongside the other three notifications
// (error logs, logins, database mutations).
//
// Today it does exactly one thing: turn "someone we don't yet recognize
// messages the bot" into a linked Telegram account, by asking them to
// share their contact and matching its phone number against
// users.phone. Every attempt to link an account — matched or not — is
// also reported to the Telegram log channel, same as every other
// notification in this app.
package telegrambot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

const greeting = "¡Hola! Soy el bot del Club de Espeleología Katiuskas. " +
	"Si eres socio o socia del club, compárteme tu contacto para identificarte."

const contactButtonText = "Compartir mi contacto"

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
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]telegram.Update, error)
}

// Bot polls Telegram for messages and reacts to them — see Run.
type Bot struct {
	client      botClient
	users       *users.Service
	logNotifier telegram.Sender
}

func New(client *telegram.Client, usersService *users.Service, logNotifier telegram.Sender) *Bot {
	return &Bot{client: client, users: usersService, logNotifier: logNotifier}
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

	// Any other message from a chat we don't recognize yet: greet and
	// ask for their contact. A chat that's already linked is left
	// alone — this package has nothing else to say to them.
	linked, err := b.users.HasTelegramChatID(ctx, msg.Chat.ID)
	if err != nil {
		slog.Error("telegram bot: check linked chat", "error", err)
		return
	}
	if linked {
		return
	}
	if err := b.client.SendMessageRequestingContact(ctx, msg.Chat.ID, greeting, contactButtonText); err != nil {
		slog.Error("telegram bot: send greeting", "error", err)
	}
}

var nonDigits = regexp.MustCompile(`\D`)

func (b *Bot) handleContact(ctx context.Context, msg *telegram.Message) {
	digits := nonDigits.ReplaceAllString(msg.Contact.PhoneNumber, "")

	matched, err := b.users.FindByPhoneSuffix(ctx, digits)
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

// notifyChannel reports every contact-share attempt to the log channel,
// matched or not — the club wants visibility into who's trying to link
// an account, not just successful ones.
func (b *Bot) notifyChannel(ctx context.Context, msg *telegram.Message, phone string, matched *users.User) {
	sender := "alguien"
	if msg.From != nil && msg.From.FirstName != "" {
		sender = msg.From.FirstName
	}
	var text string
	if matched != nil {
		text = fmt.Sprintf("📇 %s ha compartido su contacto (%s) por Telegram: coincide con %s.",
			sender, phone, matched.Login)
	} else {
		text = fmt.Sprintf("📇 %s ha compartido su contacto (%s) por Telegram: no coincide con ningún socio.",
			sender, phone)
	}
	if err := b.logNotifier.Send(ctx, text); err != nil {
		slog.Error("telegram bot: notify channel", "error", err)
	}
}

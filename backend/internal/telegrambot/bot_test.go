package telegrambot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/telegram"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func strPtr(s string) *string { return &s }

func newUsersService(t *testing.T) *users.Service {
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	svc := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	membershipsService.SetNumberAssigner(svc)
	return svc
}

// fakeClient stands in for *telegram.Client so tests never make a real
// HTTP call. batches is popped one slice per GetUpdates call — Run's
// first call (latestUpdateOffset) consumes whatever's first without
// acting on it, so tests exercising Run need an empty leading batch to
// represent "no backlog" before the batch they actually want processed.
type fakeClient struct {
	mu           sync.Mutex
	sent         []string // plain SendMessage texts
	sentContacts []string // SendMessageRequestingContact texts
	batches      [][]telegram.Update
}

func (f *fakeClient) SendMessage(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeClient) SendMessageRequestingContact(_ context.Context, _ int64, text, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentContacts = append(f.sentContacts, text)
	return nil
}

func (f *fakeClient) GetUpdates(_ context.Context, _ int64, _ int) ([]telegram.Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		return nil, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return batch, nil
}

// fakeNotifier stands in for telegram.Sender (the log channel).
type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (n *fakeNotifier) Send(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, text)
	return nil
}

// fakeMagicLinkIssuer stands in for magicLinkIssuer (*authapi.Handler in
// production) so tests can check which user id the bot asked for a
// fresh sign-in link, without pulling in all of authapi's own
// dependencies (mailer, roles, tokens...).
type fakeMagicLinkIssuer struct {
	mu        sync.Mutex
	calledFor []int64
}

func (f *fakeMagicLinkIssuer) IssueMagicLinkForTelegramUser(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calledFor = append(f.calledFor, userID)
	return nil
}

func TestHandleGreetsAnUnrecognizedChat(t *testing.T) {
	usersService := newUsersService(t)
	client := &fakeClient{}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{Chat: telegram.Chat{ID: 555}, Text: "hola"},
	})

	if len(client.sentContacts) != 1 {
		t.Fatalf("expected exactly one contact-request greeting, got %v", client.sentContacts)
	}
	if len(client.sent) != 0 {
		t.Errorf("expected no plain message sent, got %v", client.sent)
	}
	if len(notifier.sent) != 0 {
		t.Errorf("a plain greeting shouldn't notify the channel, got %v", notifier.sent)
	}
}

// TestHandleRepliesWithMagicLinkForAnAlreadyLinkedChat covers the
// requested behavior directly: someone already recognized doesn't get
// asked to link again — any message from them instead gets a fresh
// sign-in link, exactly as if they'd requested one from the web.
func TestHandleRepliesWithMagicLinkForAnAlreadyLinkedChat(t *testing.T) {
	usersService := newUsersService(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{Chat: telegram.Chat{ID: 555}, Text: "hola de nuevo"},
	})

	if len(client.sentContacts) != 0 || len(client.sent) != 0 {
		t.Errorf("expected no reply directly from the bot itself (the magic link is delivered through "+
			"authapi's own Telegram send), got contacts=%v sent=%v", client.sentContacts, client.sent)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 1 || issuer.calledFor[0] != created.ID {
		t.Errorf("expected IssueMagicLinkForTelegramUser to be called once for user %d, got %v", created.ID, issuer.calledFor)
	}
}

func TestHandleContactLinksAMatchingUser(t *testing.T) {
	usersService := newUsersService(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "García", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{
			Chat:    telegram.Chat{ID: 555},
			From:    &telegram.From{ID: 555, FirstName: "Ana"},
			Contact: &telegram.Contact{PhoneNumber: "34611222333", UserID: 555},
		},
	})

	// TelegramChatID is deliberately left out of the general User-fetching
	// query (userSelect/scanUser), so UserIDForTelegramChatID — not Get —
	// is the way to check the link took.
	linkedUserID, err := usersService.UserIDForTelegramChatID(context.Background(), 555)
	if err != nil || linkedUserID != created.ID {
		t.Fatalf("expected the chat to be linked to %d, got id=%d err=%v", created.ID, linkedUserID, err)
	}

	if len(client.sent) != 1 {
		t.Fatalf("expected exactly one confirmation reply, got %v", client.sent)
	}
	if len(notifier.sent) != 1 {
		t.Fatalf("expected exactly one channel notification, got %v", notifier.sent)
	}
	if got := notifier.sent[0]; !strings.Contains(got, created.Login) || !strings.Contains(got, "34611222333") {
		t.Errorf("expected the channel notification to mention the login and phone, got %q", got)
	}
}

// TestHandleContactRejectsSomeoneElsesSharedContact is the security
// regression test: sharing a contact card that names a real socio's
// phone number, but isn't the sender's own Telegram contact (Contact.
// UserID doesn't match From.ID), must never link that socio's account
// to the sender's chat — otherwise anyone who knew, or made up, a
// socio's phone number could hijack their magic-link/login code.
func TestHandleContactRejectsSomeoneElsesSharedContact(t *testing.T) {
	usersService := newUsersService(t)
	victim, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "Victima", LastName: "Real", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	attackerChatID := int64(666)
	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: attackerChatID},
			From: &telegram.From{ID: attackerChatID, FirstName: "Atacante"},
			// UserID belongs to someone else (or nobody at all — a
			// manually typed vCard would also carry UserID: 0), not to
			// the attacker sending this message.
			Contact: &telegram.Contact{PhoneNumber: "34611222333", UserID: 12345},
		},
	})

	if _, err := usersService.UserIDForTelegramChatID(context.Background(), attackerChatID); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("expected the attacker's chat to remain unlinked, got err=%v", err)
	}
	stillHasNoChat, err := usersService.FindByPhoneNumber(context.Background(), "34611222333")
	if err != nil {
		t.Fatalf("FindByPhoneNumber: %v", err)
	}
	if stillHasNoChat.Login != victim.Login {
		t.Fatalf("sanity check failed: phone lookup found %q, not the victim %q", stillHasNoChat.Login, victim.Login)
	}

	if len(client.sent) != 1 || !strings.Contains(client.sent[0], "tu propio contacto") {
		t.Errorf("expected a rejection reply, got %v", client.sent)
	}
	if len(notifier.sent) != 1 || !strings.Contains(notifier.sent[0], "no es el suyo") {
		t.Errorf("expected a channel warning about the mismatched contact, got %v", notifier.sent)
	}
}

func TestIsOwnContact(t *testing.T) {
	cases := []struct {
		name string
		msg  *telegram.Message
		want bool
	}{
		{
			name: "matches",
			msg: &telegram.Message{
				From:    &telegram.From{ID: 42},
				Contact: &telegram.Contact{UserID: 42},
			},
			want: true,
		},
		{
			name: "someone else's contact",
			msg: &telegram.Message{
				From:    &telegram.From{ID: 42},
				Contact: &telegram.Contact{UserID: 99},
			},
			want: false,
		},
		{
			name: "manually typed vCard has no user id at all",
			msg: &telegram.Message{
				From:    &telegram.From{ID: 42},
				Contact: &telegram.Contact{UserID: 0},
			},
			want: false,
		},
		{
			name: "no From at all",
			msg: &telegram.Message{
				From:    nil,
				Contact: &telegram.Contact{UserID: 42},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOwnContact(tc.msg); got != tc.want {
				t.Errorf("isOwnContact() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleContactReportsNoMatch(t *testing.T) {
	usersService := newUsersService(t)
	client := &fakeClient{}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{
			Chat:    telegram.Chat{ID: 999},
			From:    &telegram.From{ID: 999, FirstName: "Desconocido"},
			Contact: &telegram.Contact{PhoneNumber: "34600000000", UserID: 999},
		},
	})

	if len(client.sent) != 1 {
		t.Fatalf("expected exactly one reply, got %v", client.sent)
	}
	if len(notifier.sent) != 1 {
		t.Fatalf("expected exactly one channel notification, got %v", notifier.sent)
	}
	if got := notifier.sent[0]; !strings.Contains(got, "no coincide") {
		t.Errorf("expected the channel notification to say it didn't match, got %q", got)
	}
}

func TestLatestUpdateOffsetSkipsExistingBacklogWithoutProcessingIt(t *testing.T) {
	usersService := newUsersService(t)
	client := &fakeClient{batches: [][]telegram.Update{
		{
			{UpdateID: 10, Message: &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "old message"}},
			{UpdateID: 11, Message: &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "another old one"}},
		},
	}}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	offset, err := bot.latestUpdateOffset(context.Background())
	if err != nil {
		t.Fatalf("latestUpdateOffset: %v", err)
	}
	if offset != 12 {
		t.Errorf("expected offset 12 (past the last backlogged update), got %d", offset)
	}
	if len(client.sentContacts) != 0 || len(client.sent) != 0 || len(notifier.sent) != 0 {
		t.Errorf("expected the backlog to be skipped, not acted on: contacts=%v sent=%v notified=%v",
			client.sentContacts, client.sent, notifier.sent)
	}
}

func TestRunProcessesQueuedUpdatesAndStopsOnCancel(t *testing.T) {
	usersService := newUsersService(t)
	client := &fakeClient{batches: [][]telegram.Update{
		{}, // Run's initial latestUpdateOffset call: no backlog to skip
		{{UpdateID: 10, Message: &telegram.Message{Chat: telegram.Chat{ID: 1}, Text: "hola"}}},
	}}
	notifier := &fakeNotifier{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bot.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		processed := len(client.sentContacts) > 0
		client.mu.Unlock()
		if processed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	if len(client.sentContacts) != 1 {
		t.Errorf("expected the queued update to trigger exactly one greeting, got %v", client.sentContacts)
	}
}

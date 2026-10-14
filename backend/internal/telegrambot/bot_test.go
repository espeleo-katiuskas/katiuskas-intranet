package telegrambot

import (
	"context"
	"errors"
	"fmt"
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
	mu                   sync.Mutex
	sent                 []string // plain SendMessage texts
	sentContacts         []string // SendMessageRequestingContact texts
	sentRemovingKeyboard []string // SendMessageRemovingKeyboard texts
	deleted              []struct{ chatID, messageID int64 }
	deleteErr            error // returned by DeleteMessage when set, to test the best-effort path
	batches              [][]telegram.Update
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

func (f *fakeClient) SendMessageRemovingKeyboard(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentRemovingKeyboard = append(f.sentRemovingKeyboard, text)
	return nil
}

func (f *fakeClient) DeleteMessage(_ context.Context, chatID, messageID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, struct{ chatID, messageID int64 }{chatID, messageID})
	return f.deleteErr
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
// dependencies (mailer, roles, tokens...). err, when set, is returned
// on every call instead — used to exercise handleRecognizedChat's own
// reply when the DM delivery itself fails (see
// TestHandleRepliesWithFailureNoticeWhenIssuingTheMagicLinkFails).
type fakeMagicLinkIssuer struct {
	mu        sync.Mutex
	calledFor []int64
	err       error
}

func (f *fakeMagicLinkIssuer) IssueMagicLinkForTelegramUser(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calledFor = append(f.calledFor, userID)
	return f.err
}

// seedRecentActivity makes the bot believe chatID wrote just now, so the
// next message isn't treated as coming from an idle chat (see
// idleBeforeEmailPrompt) — needed by the tests that look at the specific
// replies to a wrong email, which an idle chat doesn't get.
func seedRecentActivity(b *Bot, chatID int64) {
	b.lastMessageAt = map[int64]time.Time{chatID: time.Now()}
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

// newLinkedEligibleUser is the common setup for the emailAttemptReply
// test cases below: an already-linked chat belonging to someone
// actually eligible to sign in (see canUseTelegramLogin), so every
// difference in the bot's reply comes purely from what they sent, not
// from the eligibility gate.
func newLinkedEligibleUser(t *testing.T, chatID int64) (*users.Service, users.User) {
	t.Helper()
	usersService := newUsersService(t)
	email := testutil.UniqueEmail(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), created.ID, chatID); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}
	return usersService, created
}

// TestHandleAsksAnAlreadyLinkedChatForItsEmailFirst is the security
// regression test for the second factor: an already-recognized chat
// doesn't get a sign-in link just for sending *any* message — merely
// having the phone/session open used to be enough, which is a real step
// down from the web flow (needs phone *and* email). A message that
// isn't the account's own registered email gets a reply instead, and
// nothing is issued or deleted — this covers the "more than one word"
// case specifically (see the sibling tests below for the others).
func TestHandleAsksAnAlreadyLinkedChatForItsEmailFirst(t *testing.T) {
	usersService, _ := newLinkedEligibleUser(t, 555)

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}
	seedRecentActivity(bot, 555) // a chat that has written lately gets the specific replies

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: "hola de nuevo"},
	})

	if len(client.sent) != 1 || client.sent[0] != onlyEmailReply {
		t.Errorf("expected onlyEmailReply for a multi-word message, got %v", client.sent)
	}
	if len(client.sentContacts) != 0 {
		t.Errorf("expected no contact-share menu for an already-linked chat, got %v", client.sentContacts)
	}
	if len(client.deleted) != 0 {
		t.Errorf("expected nothing deleted before the email is verified, got %v", client.deleted)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 0 {
		t.Errorf("expected no magic link issued yet, got %v", issuer.calledFor)
	}
}

// TestHandleRecognizedChatRejectsNonTextMessages covers an attachment
// (photo, sticker, voice note...) sent while the bot is waiting on an
// email — Telegram never sends a real text message with an empty body,
// so an empty Text always means something that isn't text.
func TestHandleRecognizedChatRejectsNonTextMessages(t *testing.T) {
	usersService, _ := newLinkedEligibleUser(t, 555)

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}
	seedRecentActivity(bot, 555) // a chat that has written lately gets the specific replies

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: ""},
	})

	if len(client.sent) != 1 || client.sent[0] != nonTextEmailReply {
		t.Errorf("expected nonTextEmailReply, got %v", client.sent)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 0 {
		t.Errorf("expected no magic link issued, got %v", issuer.calledFor)
	}
}

// TestHandleRecognizedChatRejectsANonEmailWord covers a single word
// that doesn't even look like an email address (no "@") — the reply
// repeats it back so there's no doubt about what was rejected.
func TestHandleRecognizedChatRejectsANonEmailWord(t *testing.T) {
	usersService, _ := newLinkedEligibleUser(t, 555)

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}
	seedRecentActivity(bot, 555) // a chat that has written lately gets the specific replies

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: "hola"},
	})

	want := fmt.Sprintf(notAnEmailReplyFormat, "hola")
	if len(client.sent) != 1 || client.sent[0] != want {
		t.Errorf("expected %q, got %v", want, client.sent)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 0 {
		t.Errorf("expected no magic link issued, got %v", issuer.calledFor)
	}
}

// TestHandleIssuesMagicLinkAndDeletesTheMessageWhenEmailMatches is the
// success path: sending the account's own registered email deletes that
// message (so it doesn't linger in the chat history) and asks
// authapi for a fresh sign-in link for the right person.
func TestHandleIssuesMagicLinkAndDeletesTheMessageWhenEmailMatches(t *testing.T) {
	usersService := newUsersService(t)
	email := testutil.UniqueEmail(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
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

	// Different casing and surrounding whitespace — verified leniently,
	// unlike a phone number's digits.
	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 42, Chat: telegram.Chat{ID: 555}, Text: "  " + strings.ToUpper(email) + "  "},
	})

	if len(client.sent) != 0 || len(client.sentContacts) != 0 {
		t.Errorf("expected no reply directly from the bot itself (the link is delivered through "+
			"authapi's own Telegram send), got sent=%v contacts=%v", client.sent, client.sentContacts)
	}
	if len(client.deleted) != 1 || client.deleted[0].chatID != 555 || client.deleted[0].messageID != 42 {
		t.Errorf("expected the email message to be deleted, got %v", client.deleted)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 1 || issuer.calledFor[0] != created.ID {
		t.Errorf("expected IssueMagicLinkForTelegramUser to be called once for user %d, got %v", created.ID, issuer.calledFor)
	}
}

// TestHandleRepliesWithFailureNoticeWhenIssuingTheMagicLinkFails covers
// the other side of the success path above: previously, a failure from
// magicLinks.IssueMagicLinkForTelegramUser (a delivery failure inside
// authapi, or a chat-id lookup gone wrong) was only ever logged, never
// told to the person who just proved their email — leaving them with
// neither a link nor any sign that anything had gone wrong at all,
// since this DM is the only channel this request ever gets.
func TestHandleRepliesWithFailureNoticeWhenIssuingTheMagicLinkFails(t *testing.T) {
	usersService := newUsersService(t)
	email := testutil.UniqueEmail(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{err: errors.New("telegram send failed")}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 42, Chat: telegram.Chat{ID: 555}, Text: email},
	})

	issuer.mu.Lock()
	called := len(issuer.calledFor) == 1 && issuer.calledFor[0] == created.ID
	issuer.mu.Unlock()
	if !called {
		t.Fatalf("expected IssueMagicLinkForTelegramUser to be called once for user %d, got %v", created.ID, issuer.calledFor)
	}
	if len(client.sent) != 1 || client.sent[0] != magicLinkDeliveryFailedMessage {
		t.Errorf("expected exactly one failure-notice reply, got %v", client.sent)
	}
}

// TestHandleRejectsSomeoneElsesEmail makes sure the check is against
// *this* chat's own linked account, not merely "is this any registered
// email" — sending a different real socio's email must not work.
func TestHandleRejectsSomeoneElsesEmail(t *testing.T) {
	usersService := newUsersService(t)
	linkedPerson, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create linked person: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), linkedPerson.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}
	someoneElseEmail := testutil.UniqueEmail(t)
	if _, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "C", LastName: "D", Email: strPtr(someoneElseEmail), Phone: strPtr("622333444"),
	}); err != nil {
		t.Fatalf("create someone else: %v", err)
	}

	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}
	seedRecentActivity(bot, 555) // a chat that has written lately gets the specific replies

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: someoneElseEmail},
	})

	want := fmt.Sprintf(unknownEmailReplyFormat, someoneElseEmail)
	if len(client.sent) != 1 || client.sent[0] != want {
		t.Errorf("expected the unknown-email reply (someone else's email must not match), got %v", client.sent)
	}
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 0 {
		t.Errorf("expected no magic link issued, got %v", issuer.calledFor)
	}
}

// TestHandleIssuesMagicLinkEvenWhenDeletingTheMessageFails covers the
// best-effort nature of the delete: it's a privacy nicety, not a
// security gate, so a failure to delete must never withhold the actual
// sign-in link.
func TestHandleIssuesMagicLinkEvenWhenDeletingTheMessageFails(t *testing.T) {
	usersService := newUsersService(t)
	email := testutil.UniqueEmail(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "A", LastName: "B", Email: strPtr(email), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := usersService.SetTelegramChatID(context.Background(), created.ID, 555); err != nil {
		t.Fatalf("SetTelegramChatID: %v", err)
	}

	client := &fakeClient{deleteErr: errors.New("message too old to delete")}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer}

	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: email},
	})

	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 1 || issuer.calledFor[0] != created.ID {
		t.Errorf("expected the magic link to still be issued despite the failed delete, got %v", issuer.calledFor)
	}
}

// TestHandleContactLinksAMatchingUser covers the eligible case: someone
// who could actually sign in gets the link confirmation followed
// immediately by emailPrompt, instead of having to send something else
// first to find out whether the bot is even usable for them.
func TestHandleContactLinksAMatchingUser(t *testing.T) {
	usersService := newUsersService(t)
	created, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "García", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
		RoleNames: []string{"admin"},
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

	// The confirmation removes the keyboard rather than using a plain
	// SendMessage — the "share your contact" button has served its
	// purpose now that the chat is linked, and leaving it up would only
	// invite a pointless second tap.
	if len(client.sentRemovingKeyboard) != 1 {
		t.Fatalf("expected exactly one confirmation reply removing the keyboard, got sent=%v removingKeyboard=%v", client.sent, client.sentRemovingKeyboard)
	}
	if len(client.sent) != 1 || client.sent[0] != emailPrompt {
		t.Errorf("expected an emailPrompt follow-up, got %v", client.sent)
	}
	if len(notifier.sent) != 1 {
		t.Fatalf("expected exactly one channel notification, got %v", notifier.sent)
	}
	if got := notifier.sent[0]; !strings.Contains(got, created.Login) || !strings.Contains(got, "34611222333") {
		t.Errorf("expected the channel notification to mention the login and phone, got %q", got)
	}
}

// TestHandleContactLinksAnIneligibleUser covers the other half: someone
// linked successfully (their contact really did match a real socio) but
// who isn't actually eligible to sign in — no baseline role here — gets
// a single combined reply (name, linked, but not authorized) instead of
// the plain confirmation followed by ever being asked for their email,
// which would be a dead end for them.
func TestHandleContactLinksAnIneligibleUser(t *testing.T) {
	usersService := newUsersService(t)
	if _, err := usersService.Create(context.Background(), users.CreateInput{
		FirstName: "Ana", LastName: "García", Email: strPtr(testutil.UniqueEmail(t)), Phone: strPtr("611222333"),
	}); err != nil {
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

	want := fmt.Sprintf(unauthorizedAfterLinkReplyFormat, "Ana", "García")
	if len(client.sentRemovingKeyboard) != 1 || client.sentRemovingKeyboard[0] != want {
		t.Errorf("expected the combined linked-but-unauthorized reply, got %v", client.sentRemovingKeyboard)
	}
	if len(client.sent) != 0 {
		t.Errorf("expected no separate follow-up message, got %v", client.sent)
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

	// Same reasoning as the matched case: it's still their own contact,
	// so tapping the button again would only resend the same phone
	// number and get the same "no match" answer — the keyboard comes
	// down here too.
	if len(client.sentRemovingKeyboard) != 1 {
		t.Fatalf("expected exactly one reply removing the keyboard, got sent=%v removingKeyboard=%v", client.sent, client.sentRemovingKeyboard)
	}
	if !strings.Contains(client.sentRemovingKeyboard[0], contactClubHint) {
		t.Errorf("expected the no-match reply to suggest contacting the club, got %q", client.sentRemovingKeyboard[0])
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

// TestShouldEscalateGetUpdatesFailure covers the actual behavior
// change: an isolated GetUpdates failure (a Telegram long-poll blip)
// doesn't escalate to ERROR — only the one at exactly
// getUpdatesFailureLogThreshold does, and none of the ones after that,
// so a longer outage doesn't keep re-notifying the log channel for as
// long as it lasts.
func TestShouldEscalateGetUpdatesFailure(t *testing.T) {
	cases := []struct {
		consecutiveFailures int
		want                bool
	}{
		{1, false},
		{2, false},
		{3, true},
		{4, false},
		{5, false},
	}
	for _, c := range cases {
		if got := shouldEscalateGetUpdatesFailure(c.consecutiveFailures); got != c.want {
			t.Errorf("shouldEscalateGetUpdatesFailure(%d) = %v, want %v", c.consecutiveFailures, got, c.want)
		}
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

// A message that isn't the email, from a chat that has been silent for
// more than idleBeforeEmailPrompt — or that the bot has never heard from
// since it started — gets the plain emailPrompt, whatever specific error
// it would otherwise have got: the person most likely doesn't remember
// that their email is what the bot expects. A chat that wrote recently
// still gets the specific reply.
func TestHandleRepliesWithEmailPromptToAnIdleChat(t *testing.T) {
	usersService, linkedUser := newLinkedEligibleUser(t, 555)
	client := &fakeClient{}
	notifier := &fakeNotifier{}
	issuer := &fakeMagicLinkIssuer{}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	bot := &Bot{client: client, users: usersService, logNotifier: notifier, magicLinks: issuer, now: func() time.Time { return now }}
	say := func(text string) string {
		t.Helper()
		before := len(client.sent)
		bot.handle(context.Background(), telegram.Update{
			Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: 555}, Text: text},
		})
		if len(client.sent) != before+1 {
			t.Fatalf("expected exactly one reply to %q, got %v", text, client.sent[before:])
		}
		return client.sent[len(client.sent)-1]
	}

	if got := say("hola de nuevo"); got != emailPrompt {
		t.Errorf("a chat the bot has never heard from should get emailPrompt, got %q", got)
	}

	now = now.Add(5 * time.Minute)
	if got := say("hola de nuevo"); got != onlyEmailReply {
		t.Errorf("a chat that wrote 5 minutes ago should get the specific reply, got %q", got)
	}

	now = now.Add(idleBeforeEmailPrompt) // exactly 30 minutes since the last message: not yet idle
	if got := say("hola"); got != fmt.Sprintf(notAnEmailReplyFormat, "hola") {
		t.Errorf("exactly at the limit still counts as recent, got %q", got)
	}

	now = now.Add(idleBeforeEmailPrompt + time.Second)
	if got := say("otra cosa"); got != emailPrompt {
		t.Errorf("after more than 30 silent minutes expected emailPrompt, got %q", got)
	}

	// Every message counts as activity, so right after the prompt the next
	// wrong message is a recent one again.
	now = now.Add(time.Minute)
	if got := say("hola otra vez"); got != onlyEmailReply {
		t.Errorf("expected the specific reply again once the chat is active, got %q", got)
	}

	// And an idle chat that does send its email is not held back by this.
	now = now.Add(2 * idleBeforeEmailPrompt)
	bot.handle(context.Background(), telegram.Update{
		Message: &telegram.Message{MessageID: 2, Chat: telegram.Chat{ID: 555}, Text: *linkedUser.Email},
	})
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if len(issuer.calledFor) != 1 {
		t.Errorf("expected the right email from an idle chat to still issue the link, got %v", issuer.calledFor)
	}
}

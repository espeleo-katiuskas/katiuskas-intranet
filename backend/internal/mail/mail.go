// Package mail sends the emails the API needs to hand out — currently just
// magic-link sign-in links. Without SMTP configured it logs the message
// instead of sending it, which is enough to develop and test against: the
// link is right there in the API's logs.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// Sender's to is one or more addresses, comma-separated when there's
// more than one (the caller's job — see authapi.Handler — since a
// person's stored contact fields hold their own, differently-separated
// convention).
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// ConsoleSender logs the email instead of sending it. It's the default
// when SMTP_HOST isn't set, which is the right default for local
// development and for a fresh deployment nobody has configured SMTP for
// yet — better to be loudly unable to send email than to silently drop a
// sign-in link.
type ConsoleSender struct{}

func (ConsoleSender) Send(_ context.Context, to, subject, body string) error {
	slog.Info("email not sent: no SMTP_HOST configured, logging instead",
		"to", to, "subject", subject, "body", body)
	return nil
}

// SMTPSender sends real email over SMTP with PLAIN auth (e.g. against a
// provider like Mailgun, SES's SMTP endpoint, or an internal relay).
type SMTPSender struct {
	Host, Port, Username, Password, From string
}

func (s SMTPSender) Send(_ context.Context, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%s", s.Host, s.Port)
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.From, to, subject, body)
	return smtp.SendMail(addr, auth, s.From, splitRecipients(to), []byte(msg))
}

// splitRecipients turns to's comma-separated address list into the
// individual envelope recipients SMTP's RCPT TO needs — the To: header
// accepts the joined string as-is, but one combined string there would
// be sent (and likely rejected) as a single, invalid mailbox.
func splitRecipients(to string) []string {
	recipients := strings.Split(to, ",")
	for i, r := range recipients {
		recipients[i] = strings.TrimSpace(r)
	}
	return recipients
}

// NewSenderFromConfig returns an SMTPSender when host/port/from are all
// set, and a ConsoleSender otherwise.
func NewSenderFromConfig(host, port, username, password, from string) Sender {
	if host == "" || port == "" || from == "" {
		return ConsoleSender{}
	}
	return SMTPSender{Host: host, Port: port, Username: username, Password: password, From: from}
}

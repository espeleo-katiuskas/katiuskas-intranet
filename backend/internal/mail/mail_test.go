package mail

import (
	"context"
	"testing"
)

func TestNewSenderFromConfigDefaultsToConsole(t *testing.T) {
	cases := []struct {
		name                             string
		host, port, username, pass, from string
	}{
		{"nothing set", "", "", "", "", ""},
		{"missing host", "", "587", "", "", "from@example.com"},
		{"missing port", "smtp.example.com", "", "", "", "from@example.com"},
		{"missing from", "smtp.example.com", "587", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := NewSenderFromConfig(tc.host, tc.port, tc.username, tc.pass, tc.from)
			if _, ok := sender.(ConsoleSender); !ok {
				t.Errorf("expected ConsoleSender, got %T", sender)
			}
		})
	}
}

func TestNewSenderFromConfigReturnsSMTPWhenFullyConfigured(t *testing.T) {
	sender := NewSenderFromConfig("smtp.example.com", "587", "user", "pass", "from@example.com")
	smtpSender, ok := sender.(SMTPSender)
	if !ok {
		t.Fatalf("expected SMTPSender, got %T", sender)
	}
	if smtpSender.Host != "smtp.example.com" || smtpSender.Port != "587" ||
		smtpSender.Username != "user" || smtpSender.Password != "pass" || smtpSender.From != "from@example.com" {
		t.Errorf("SMTPSender fields not wired correctly: %+v", smtpSender)
	}
}

func TestConsoleSenderNeverErrors(t *testing.T) {
	if err := (ConsoleSender{}).Send(context.Background(), "someone@example.com", "subject", "body"); err != nil {
		t.Errorf("expected ConsoleSender.Send to never error, got %v", err)
	}
}

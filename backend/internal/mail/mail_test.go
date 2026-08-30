package mail

import (
	"context"
	"reflect"
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

func TestSplitRecipientsHandlesOneOrSeveralAddresses(t *testing.T) {
	cases := []struct {
		to   string
		want []string
	}{
		{"a@example.com", []string{"a@example.com"}},
		{"a@example.com, b@example.com", []string{"a@example.com", "b@example.com"}},
		{"a@example.com,b@example.com,c@example.com", []string{"a@example.com", "b@example.com", "c@example.com"}},
	}
	for _, tc := range cases {
		if got := splitRecipients(tc.to); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitRecipients(%q) = %v, want %v", tc.to, got, tc.want)
		}
	}
}

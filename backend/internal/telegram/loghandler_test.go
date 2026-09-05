package telegram

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeSender) waitForMessage(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a message to be sent")
	return ""
}

func TestLogHandlerForwardsRecordsAtOrAboveMinLevel(t *testing.T) {
	var out bytes.Buffer
	sender := &fakeSender{}
	handler := NewLogHandler(slog.NewTextHandler(&out, nil), sender, slog.LevelError)
	logger := slog.New(handler)

	logger.Error("something broke", "user", "alice")

	msg := sender.waitForMessage(t)
	if !strings.Contains(msg, "something broke") || !strings.Contains(msg, "alice") {
		t.Errorf("forwarded message missing expected content: %q", msg)
	}
	if !strings.Contains(out.String(), "something broke") {
		t.Errorf("wrapped handler didn't receive the record: %q", out.String())
	}
}

func TestLogHandlerDoesNotForwardRecordsBelowMinLevel(t *testing.T) {
	var out bytes.Buffer
	sender := &fakeSender{}
	handler := NewLogHandler(slog.NewTextHandler(&out, nil), sender, slog.LevelError)
	logger := slog.New(handler)

	logger.Info("just fyi")
	time.Sleep(50 * time.Millisecond)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Errorf("expected no message sent, got %v", sender.sent)
	}
	if !strings.Contains(out.String(), "just fyi") {
		t.Errorf("wrapped handler didn't receive the record: %q", out.String())
	}
}

func TestLogHandlerWithAttrsCarriesAttrsIntoForwardedMessages(t *testing.T) {
	var out bytes.Buffer
	sender := &fakeSender{}
	handler := NewLogHandler(slog.NewTextHandler(&out, nil), sender, slog.LevelError)
	logger := slog.New(handler).With("request_id", "abc123")

	logger.Error("boom")

	msg := sender.waitForMessage(t)
	if !strings.Contains(msg, "request_id: abc123") {
		t.Errorf("forwarded message missing attrs from With(): %q", msg)
	}
}

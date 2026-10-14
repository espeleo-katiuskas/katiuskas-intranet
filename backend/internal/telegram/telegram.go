// Package telegram sends messages to a Telegram chat through the Bot HTTP
// API (https://core.telegram.org/bots/api) — plain net/http and
// encoding/json, no SDK. Used today to forward the API's own error-level
// logs to a club Telegram group or channel (see LogHandler), so someone
// who isn't tailing `docker compose logs api` still finds out when
// something breaks. Without TELEGRAM_BOT_TOKEN/TELEGRAM_LOG_CHAT_ID
// configured, it logs instead of sending — the same fallback
// internal/mail uses for SMTP.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Sender sends a single message to whichever chat it's configured for —
// currently always one fixed chat (a group or channel for logs), so
// there's no destination parameter: see BotSender.ChatID.
type Sender interface {
	Send(ctx context.Context, text string) error
}

// NoopSender logs the message instead of sending it. It's the default
// when TELEGRAM_BOT_TOKEN or TELEGRAM_LOG_CHAT_ID isn't set, which is the
// right default for local development and for a fresh deployment nobody
// has configured a bot for yet.
type NoopSender struct{}

func (NoopSender) Send(_ context.Context, text string) error {
	slog.Info("telegram message not sent: no TELEGRAM_BOT_TOKEN/TELEGRAM_LOG_CHAT_ID configured, logging instead",
		"text", text)
	return nil
}

// BotSender sends a message through a Telegram bot's sendMessage
// endpoint. ChatID identifies the destination the same way Telegram
// itself does: a negative number for a group/supergroup, or "@name" for
// a public channel/group's username.
type BotSender struct {
	Token, ChatID string
	// HTTPClient defaults to a client with a 10s timeout when nil — never
	// left to hang indefinitely on an unreachable Telegram API.
	HTTPClient *http.Client
	// sendMessageURL overrides the real Telegram API endpoint; only ever
	// set in tests, to point at an httptest server instead.
	sendMessageURL string
}

type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	// Result's shape depends entirely on which Telegram method was
	// called — a Message object for sendMessage, but a bare `true` for
	// deleteMessage — and Client.postResult is shared by every method
	// Client has, so this has to stay undecoded here rather than typed
	// as Message: a fixed struct type failed to unmarshal deleteMessage's
	// own boolean result, breaking every DeleteMessage call outright
	// (found in production — the email-verification and expired-
	// magic-link cleanups both use it). Only SendMessageForID actually
	// needs to look inside Result, and does its own second unmarshal
	// into Message there — every other caller of post/postResult never
	// touches this field at all, so nothing about their own response
	// shape can break decoding for them either.
	Result json.RawMessage `json:"result"`
}

func (b BotSender) Send(ctx context.Context, text string) error {
	body, err := json.Marshal(map[string]string{
		"chat_id": b.ChatID,
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	url := b.sendMessageURL
	if url == "" {
		url = "https://api.telegram.org/bot" + b.Token + "/sendMessage"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := b.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	var parsed sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram API error: %s", parsed.Description)
	}
	return nil
}

var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// NewSenderFromConfig returns a BotSender when token and chatID are both
// set, and a NoopSender otherwise.
func NewSenderFromConfig(token, chatID string) Sender {
	if token == "" || chatID == "" {
		return NoopSender{}
	}
	return BotSender{Token: token, ChatID: chatID}
}

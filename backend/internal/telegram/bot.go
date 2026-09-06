package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is a general Telegram Bot API client, not tied to any one chat
// — unlike Sender/BotSender above, which exist only to plug into the
// chat-agnostic notifications elsewhere in this codebase (error logs,
// logins, database mutations) that all target the same fixed log chat.
// This is what internal/telegrambot uses to talk to whoever messages the
// bot, and to poll for their messages in the first place.
type Client struct {
	Token      string
	HTTPClient *http.Client
	// apiBase overrides the real Telegram API's base URL; only ever set
	// in tests, to point at an httptest server instead.
	apiBase string
}

func NewClient(token string) *Client {
	return &Client{Token: token}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

func (c *Client) base() string {
	if c.apiBase != "" {
		return c.apiBase
	}
	return "https://api.telegram.org"
}

// SendMessage sends a plain text message to chatID.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	return c.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
}

// SendMessageRequestingContact sends text along with a keyboard offering
// a single button that, when tapped, shares the person's own Telegram
// contact (their phone number) back to the bot — see
// internal/telegrambot, which matches that phone against users.phone.
func (c *Client) SendMessageRequestingContact(ctx context.Context, chatID int64, text, buttonText string) error {
	return c.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]any{
			"keyboard": [][]map[string]any{
				{{"text": buttonText, "request_contact": true}},
			},
			"resize_keyboard":   true,
			"one_time_keyboard": true,
		},
	})
}

// DeleteMessage removes a message the bot itself received in a private
// chat — Telegram's Bot API allows this specifically so a bot can clean
// up sensitive text (an OTP, a password typed by mistake, ...) a person
// sent it, which is exactly what internal/telegrambot uses this for: a
// message that turned out to carry someone's own email, once it's
// served its purpose confirming who they are, shouldn't linger in the
// chat history for whoever might get hold of the phone/session later.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.post(ctx, "deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	})
}

func (c *Client) post(ctx context.Context, method string, params map[string]any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/%s", c.base(), c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
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

// Chat is the chat a message arrived in — ID doubles as the person's own
// Telegram user ID for a private chat with the bot, which is the only
// kind this codebase talks to.
type Chat struct {
	ID int64 `json:"id"`
}

// From identifies whoever sent a message — just enough to personalize a
// greeting, not a general Telegram user record.
type From struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Contact is what arrives when someone shares their (or someone else's)
// contact with the bot — see SendMessageRequestingContact.
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	// UserID is set only when the shared contact is a Telegram user
	// (never for a plain phone number typed into a contact card), and
	// only used here to make sure someone isn't sharing a contact card
	// that isn't their own — see internal/telegrambot.
	UserID int64 `json:"user_id"`
}

type Message struct {
	MessageID int64    `json:"message_id"`
	Chat      Chat     `json:"chat"`
	From      *From    `json:"from"`
	Text      string   `json:"text"`
	Contact   *Contact `json:"contact"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type getUpdatesResponse struct {
	OK          bool     `json:"ok"`
	Description string   `json:"description"`
	Result      []Update `json:"result"`
}

// GetUpdates long-polls Telegram for new messages: the request itself
// blocks server-side for up to timeoutSeconds waiting for one to arrive,
// so the HTTP client here always allows a bit longer than that rather
// than the fixed 10s defaultHTTPClient uses everywhere else. offset is
// the next update id to fetch — pass the highest UpdateID seen so far,
// plus one, so nothing is delivered twice (see telegrambot.Bot.Run).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	url := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=%d", c.base(), c.Token, offset, timeoutSeconds)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: time.Duration(timeoutSeconds+10) * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get updates: %w", err)
	}
	defer resp.Body.Close()

	var parsed getUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram API error: %s", parsed.Description)
	}
	return parsed.Result, nil
}

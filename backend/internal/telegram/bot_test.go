package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendMessage(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	if err := client.SendMessage(context.Background(), 42, "hola"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if gotPath != "/bot123:abc/sendMessage" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotBody["chat_id"] != float64(42) || gotBody["text"] != "hola" {
		t.Errorf("unexpected request body: %v", gotBody)
	}
	if _, hasKeyboard := gotBody["reply_markup"]; hasKeyboard {
		t.Errorf("plain SendMessage should not include a keyboard: %v", gotBody)
	}
}

func TestClientSendMessageForIDReturnsTheSentMessagesID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"message_id": 99, "chat": map[string]any{"id": 42}},
		})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	messageID, err := client.SendMessageForID(context.Background(), 42, "hola")
	if err != nil {
		t.Fatalf("SendMessageForID: %v", err)
	}
	if messageID != 99 {
		t.Errorf("expected message id 99, got %d", messageID)
	}
}

// TestClientDeleteMessage's fake server deliberately responds with the
// real Telegram API's own shape for a successful deleteMessage — result
// is a bare `true`, not a Message object the way sendMessage's own
// result is — since a fake that just returned {"ok": true} (no result
// at all) previously let a real bug slip through untested: decoding
// result as a Message unconditionally broke on the bool Telegram
// actually sends back.
func TestClientDeleteMessage(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	if err := client.DeleteMessage(context.Background(), 42, 99); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if gotPath != "/bot123:abc/deleteMessage" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotBody["chat_id"] != float64(42) || gotBody["message_id"] != float64(99) {
		t.Errorf("unexpected request body: %v", gotBody)
	}
}

func TestClientSendMessageRequestingContactIncludesAKeyboard(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	if err := client.SendMessageRequestingContact(context.Background(), 42, "hola", "Compartir contacto"); err != nil {
		t.Fatalf("SendMessageRequestingContact: %v", err)
	}
	markup, ok := gotBody["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("expected a reply_markup object, got %v", gotBody["reply_markup"])
	}
	keyboard, ok := markup["keyboard"].([]any)
	if !ok || len(keyboard) != 1 {
		t.Fatalf("expected a one-row keyboard, got %v", markup["keyboard"])
	}
	row, ok := keyboard[0].([]any)
	if !ok || len(row) != 1 {
		t.Fatalf("expected a one-button row, got %v", keyboard[0])
	}
	button := row[0].(map[string]any)
	if button["text"] != "Compartir contacto" || button["request_contact"] != true {
		t.Errorf("unexpected button: %v", button)
	}
	if markup["is_persistent"] != true {
		t.Errorf("expected is_persistent so the keyboard isn't hidden behind the keyboard-icon toggle, got %v", markup["is_persistent"])
	}
}

func TestClientSendMessageRemovingKeyboardIncludesRemoveKeyboard(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	if err := client.SendMessageRemovingKeyboard(context.Background(), 42, "gracias"); err != nil {
		t.Fatalf("SendMessageRemovingKeyboard: %v", err)
	}
	if gotBody["text"] != "gracias" {
		t.Errorf("unexpected text: %v", gotBody["text"])
	}
	markup, ok := gotBody["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("expected a reply_markup object, got %v", gotBody["reply_markup"])
	}
	if markup["remove_keyboard"] != true {
		t.Errorf("expected remove_keyboard: true, got %v", markup)
	}
}

func TestClientSendMessageReturnsErrorOnTelegramAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "chat not found"})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	if err := client.SendMessage(context.Background(), 42, "hola"); err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestClientGetUpdatesParsesMessagesAndContacts(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []map[string]any{
				{
					"update_id": 100,
					"message": map[string]any{
						"chat":       map[string]any{"id": 42},
						"from":       map[string]any{"id": 42, "first_name": "Ana"},
						"text":       "hola",
						"message_id": 7,
					},
				},
				{
					"update_id": 101,
					"message": map[string]any{
						"chat":    map[string]any{"id": 42},
						"contact": map[string]any{"phone_number": "34611222333", "user_id": 42},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
	updates, err := client.GetUpdates(context.Background(), 50, 1)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if gotQuery != "offset=50&timeout=1" {
		t.Errorf("unexpected query string: %s", gotQuery)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	if updates[0].Message.Text != "hola" || updates[0].Message.From.FirstName != "Ana" || updates[0].Message.MessageID != 7 {
		t.Errorf("unexpected first update: %+v", updates[0])
	}
	if updates[1].Message.Contact == nil || updates[1].Message.Contact.PhoneNumber != "34611222333" {
		t.Errorf("unexpected second update: %+v", updates[1])
	}
}

// A failed call is an *APIError carrying Telegram's own error_code and
// description, and IsUnreachable tells "the bot can't talk to this chat at
// all" apart from failures worth retrying: any 403 Forbidden (blocked the
// bot, closed or deactivated their account...), or "chat not found" /
// "user not found".
func TestIsUnreachable(t *testing.T) {
	for _, c := range []struct {
		code        int
		description string
		want        bool
	}{
		{403, "Forbidden: bot was blocked by the user", true},
		{403, "Forbidden: user is deactivated", true},
		{403, "Forbidden: some wording nobody has seen yet", true},
		{400, "Bad Request: chat not found", true},
		{400, "Bad Request: user not found", true},
		{400, "BAD REQUEST: CHAT NOT FOUND", true},
		{0, "Bad Request: chat not found", true}, // no error_code sent at all
		{429, "Too Many Requests: retry after 5", false},
		{400, "Bad Request: message text is empty", false},
		{500, "Internal Server Error", false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			body := map[string]any{"ok": false, "description": c.description}
			if c.code != 0 {
				body["error_code"] = c.code
			}
			json.NewEncoder(w).Encode(body)
		}))
		client := &Client{Token: "123:abc", HTTPClient: server.Client(), apiBase: server.URL}
		_, err := client.SendMessageForID(context.Background(), 42, "hola")
		server.Close()

		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Description != c.description || apiErr.ErrorCode != c.code {
			t.Errorf("%d %q: expected an *APIError with that code and description, got %v", c.code, c.description, err)
		}
		if got := IsUnreachable(err); got != c.want {
			t.Errorf("%d %q: expected IsUnreachable %v, got %v", c.code, c.description, c.want, got)
		}
	}
	if IsUnreachable(errors.New("chat not found")) {
		t.Error("expected a plain error that merely mentions it not to count: only Telegram's own answer does")
	}
	if !IsUnreachable(fmt.Errorf("wrapped: %w", &APIError{Description: "chat not found"})) {
		t.Error("expected a wrapped APIError to still count")
	}
	if IsUnreachable(nil) {
		t.Error("expected nil not to count")
	}
}

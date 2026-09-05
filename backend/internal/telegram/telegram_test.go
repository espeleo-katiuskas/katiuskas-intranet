package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewSenderFromConfigDefaultsToNoop(t *testing.T) {
	cases := []struct {
		name, token, chatID string
	}{
		{"nothing set", "", ""},
		{"missing token", "", "-1001234567890"},
		{"missing chat id", "123:abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := NewSenderFromConfig(tc.token, tc.chatID)
			if _, ok := sender.(NoopSender); !ok {
				t.Errorf("expected NoopSender, got %T", sender)
			}
		})
	}
}

func TestNewSenderFromConfigReturnsBotSenderWhenFullyConfigured(t *testing.T) {
	sender := NewSenderFromConfig("123:abc", "-1001234567890")
	botSender, ok := sender.(BotSender)
	if !ok {
		t.Fatalf("expected BotSender, got %T", sender)
	}
	if botSender.Token != "123:abc" || botSender.ChatID != "-1001234567890" {
		t.Errorf("BotSender fields not wired correctly: %+v", botSender)
	}
}

func TestNoopSenderNeverErrors(t *testing.T) {
	if err := (NoopSender{}).Send(context.Background(), "hello"); err != nil {
		t.Errorf("expected NoopSender.Send to never error, got %v", err)
	}
}

func TestBotSenderSendPostsToTelegramAPI(t *testing.T) {
	var gotPath string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	sender := BotSender{Token: "123:abc", ChatID: "-1001234567890", HTTPClient: server.Client()}
	sender.sendMessageURL = server.URL + "/bot123:abc/sendMessage"

	if err := sender.Send(context.Background(), "hola"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/bot123:abc/sendMessage" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if gotBody["chat_id"] != "-1001234567890" || gotBody["text"] != "hola" {
		t.Errorf("unexpected request body: %v", gotBody)
	}
}

func TestBotSenderSendReturnsErrorOnTelegramAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "chat not found"})
	}))
	defer server.Close()

	sender := BotSender{Token: "123:abc", ChatID: "bad-chat", HTTPClient: server.Client()}
	sender.sendMessageURL = server.URL + "/bot123:abc/sendMessage"

	err := sender.Send(context.Background(), "hola")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

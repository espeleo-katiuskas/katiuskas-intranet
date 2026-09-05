package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// MutationNotifier is told about every state-changing API call — see
// NotifyMutatingCalls. Defined locally, the same way other packages
// narrow a dependency to just what they need (a "UserLookup" interface,
// say) rather than importing internal/telegram directly; telegram.Sender
// satisfies this structurally without either package knowing about the
// other.
type MutationNotifier interface {
	Send(ctx context.Context, text string) error
}

// NotifyMutatingCalls reports every POST/PUT/PATCH/DELETE request to
// notifier once it completes, regardless of the response status — a
// failed write is still worth knowing about, and this middleware isn't
// the place to decide which failures matter. Sending is fire-and-forget
// on its own goroutine with a bounded timeout and a background context
// detached from the request (which has already finished by the time the
// Telegram call would complete) — the same shape every other Telegram
// notification in this app uses. A nil notifier disables this
// middleware entirely; see cmd/api/main.go, which only builds one when
// TELEGRAM_BOT_TOKEN/TELEGRAM_LOG_CHAT_ID are actually configured.
func NotifyMutatingCalls(notifier MutationNotifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if notifier == nil || !isMutatingMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			method, path, status := r.Method, r.URL.Path, rec.status
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				text := fmt.Sprintf("[API] %s %s -> %d", method, path, status)
				if err := notifier.Send(ctx, text); err != nil {
					slog.Error("notify api call to telegram", "error", err)
				}
			}()
		})
	}
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

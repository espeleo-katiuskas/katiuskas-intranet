package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"
)

// LogHandler wraps another slog.Handler, additionally forwarding every
// record at or above MinLevel to Telegram — the record is always passed
// through to the wrapped handler unchanged either way, so this only adds
// a side channel, never changes what gets logged to stdout. Sending runs
// on its own goroutine with a bounded timeout, so a slow or unreachable
// Telegram API never blocks whatever's doing the logging; a send failure
// is written straight to stderr rather than through slog, since logging
// it that way would recurse right back into this same handler.
type LogHandler struct {
	next     slog.Handler
	sender   Sender
	minLevel slog.Level
	attrs    []slog.Attr
}

func NewLogHandler(next slog.Handler, sender Sender, minLevel slog.Level) *LogHandler {
	return &LogHandler{next: next, sender: sender, minLevel: minLevel}
}

func (h *LogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *LogHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= h.minLevel {
		go h.forward(record)
	}
	return h.next.Handle(ctx, record)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{
		next:     h.next.WithAttrs(attrs),
		sender:   h.sender,
		minLevel: h.minLevel,
		attrs:    append(slices.Clone(h.attrs), attrs...),
	}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{next: h.next.WithGroup(name), sender: h.sender, minLevel: h.minLevel, attrs: h.attrs}
}

func (h *LogHandler) forward(record slog.Record) {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", record.Level, record.Message)
	for _, a := range h.attrs {
		fmt.Fprintf(&b, "\n%s: %s", a.Key, summarize(a.Value.String()))
	}
	record.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, "\n%s: %s", a.Key, summarize(a.Value.String()))
		return true
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.sender.Send(ctx, b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "telegram: forward log record: %v\n", err)
	}
}

// maxAttrLines bounds how much of a multi-line attribute value (chiefly
// a panic's full goroutine stack trace — see httpapi/middleware.go) goes
// into a Telegram message: just enough to see where things broke, not a
// wall of text. The full value is unaffected everywhere else — this only
// trims what gets forwarded here.
const maxAttrLines = 6

func summarize(value string) string {
	lines := strings.Split(value, "\n")
	if len(lines) <= maxAttrLines {
		return value
	}
	omitted := len(lines) - maxAttrLines
	return strings.Join(lines[:maxAttrLines], "\n") + fmt.Sprintf("\n… (%d more lines omitted)", omitted)
}

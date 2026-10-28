// Package logging has the slog handler the commands log through: one plain line per record,
// made to be read by a person (in a terminal or with journalctl), not parsed by a tool.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Handler writes each record as
//
//	2026-10-28 16:10:32 INFO  message key=value key="a value with spaces"
//
// A value that spans several lines (a stack trace, the body of an email) is not squeezed into
// the line: it is written below it, indented, under its key.
type Handler struct {
	out      io.Writer
	mu       *sync.Mutex
	level    slog.Leveler
	withTime bool
	attrs    []slog.Attr // from WithAttrs, already prefixed with their groups
	groups   []string
}

// NewHandler returns a Handler that logs records at level or above to out. withTime says
// whether each line starts with the date and time: leave it out where the reader adds its own,
// as the systemd journal does.
func NewHandler(out io.Writer, level slog.Leveler, withTime bool) *Handler {
	return &Handler{out: out, mu: new(sync.Mutex), level: level, withTime: withTime}
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		c.attrs = append(c.attrs, h.prefixed(a))
	}
	return &c
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.groups = append(append([]string(nil), h.groups...), name)
	return &c
}

func (h *Handler) prefixed(a slog.Attr) slog.Attr {
	if len(h.groups) > 0 {
		a.Key = strings.Join(h.groups, ".") + "." + a.Key
	}
	return a
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var line, blocks strings.Builder
	if h.withTime && !r.Time.IsZero() {
		line.WriteString(r.Time.Local().Format(time.DateTime))
		line.WriteByte(' ')
	}
	line.WriteString(levelName(r.Level))
	line.WriteByte(' ')
	line.WriteString(r.Message)

	add := func(a slog.Attr) {
		h.write(&line, &blocks, a, "")
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(h.prefixed(a))
		return true
	})
	line.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line.String()+blocks.String())
	return err
}

// write adds one attribute: to the line if it fits in it, else as an indented block.
func (h *Handler) write(line, blocks *strings.Builder, a slog.Attr, prefix string) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := prefix + a.Key
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix = key + "."
		}
		for _, g := range a.Value.Group() {
			h.write(line, blocks, g, prefix)
		}
		return
	}
	s := valueString(a.Value)
	if strings.Contains(s, "\n") {
		blocks.WriteString("    " + key + ":\n")
		for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			if l != "" {
				blocks.WriteString("        " + l)
			}
			blocks.WriteByte('\n')
		}
		return
	}
	line.WriteByte(' ')
	line.WriteString(key)
	line.WriteByte('=')
	if needsQuotes(s) {
		s = strconv.Quote(s)
	}
	line.WriteString(s)
}

func valueString(v slog.Value) string {
	if v.Kind() == slog.KindTime {
		return v.Time().Local().Format(time.DateTime)
	}
	return v.String()
}

func needsQuotes(s string) bool {
	if s == "" {
		return true
	}
	for _, c := range s {
		if c == ' ' || c == '"' || c == '=' || c < ' ' || c == 0x7f {
			return true
		}
	}
	return false
}

// levelName pads the level so the messages line up.
func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN "
	case l >= slog.LevelInfo:
		return "INFO "
	default:
		return "DEBUG"
	}
}

// New returns the logger the commands use: to standard output, at Info level, without the time
// when running under systemd (JOURNAL_STREAM is set), since the journal stamps every line.
func New() *slog.Logger {
	return slog.New(NewHandler(os.Stdout, slog.LevelInfo, os.Getenv("JOURNAL_STREAM") == ""))
}

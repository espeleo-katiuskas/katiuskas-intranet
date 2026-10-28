package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLineFormat(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo, false))
	l.Info("GET /users", "status", 200, "who", "Ana García", "error", errors.New("boom"))
	want := "INFO  GET /users status=200 who=\"Ana García\" error=boom\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestTimePrefix(t *testing.T) {
	var buf bytes.Buffer
	slog.New(NewHandler(&buf, slog.LevelInfo, true)).Warn("hello")
	// "2006-01-02 15:04:05 WARN  hello"
	if len(buf.String()) != len("2006-01-02 15:04:05 WARN  hello\n") || !strings.HasSuffix(buf.String(), " WARN  hello\n") {
		t.Errorf("unexpected line %q", buf.String())
	}
}

func TestMultilineValueGoesBelowTheLine(t *testing.T) {
	var buf bytes.Buffer
	slog.New(NewHandler(&buf, slog.LevelInfo, false)).Error("panic recovered", "error", "x", "stack", "a\nb\n")
	want := "ERROR panic recovered error=x\n    stack:\n        a\n        b\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	slog.New(NewHandler(&buf, slog.LevelWarn, false)).Info("quiet")
	if buf.Len() != 0 {
		t.Errorf("Info was logged at level Warn: %q", buf.String())
	}
}

func TestWithAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo, false)).With("a", 1).WithGroup("g")
	l.Info("m", "b", 2, slog.Group("h", "c", 3))
	want := "INFO  m a=1 g.b=2 g.h.c=3\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

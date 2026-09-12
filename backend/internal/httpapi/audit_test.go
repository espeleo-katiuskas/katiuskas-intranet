package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

// fakeMutationNotifier stands in for MutationNotifier in tests across
// this package, so they can check what was actually sent without a
// real Telegram call.
type fakeMutationNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeMutationNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

// waitForMessage polls briefly for NotifyAsync's own goroutine to have
// delivered something, rather than asserting immediately (the send is
// fire-and-forget, so it can still be in flight right after the call
// that triggered it returns).
func (f *fakeMutationNotifier) waitForMessage(t *testing.T) string {
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

func TestNotifyAsyncNilNotifierIsANoop(t *testing.T) {
	// Must not panic or block — nil is what every handler gets when
	// Telegram isn't configured at all.
	NotifyAsync(nil, "should never be sent anywhere")
}

func TestNotifyAsyncSendsToNotifier(t *testing.T) {
	notifier := &fakeMutationNotifier{}
	NotifyAsync(notifier, "hola")
	if got := notifier.waitForMessage(t); got != "hola" {
		t.Errorf("expected %q, got %q", "hola", got)
	}
}

type diffSubject struct {
	Name  string
	Phone *string
	Born  time.Time
	Until *time.Time
	Roles []string // never diffed directly in practice, but DiffFields must not choke on it if named
}

func TestDiffFieldsReportsOnlyChangedFields(t *testing.T) {
	before := diffSubject{Name: "Ana", Phone: strPtr("611222333")}
	after := diffSubject{Name: "Ana", Phone: strPtr("666777888")}

	got := DiffFields(before, after, "Name", "Phone")
	want := []string{"Phone: 611222333 → 666777888"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestDiffFieldsReturnsNilWhenNothingChanged(t *testing.T) {
	before := diffSubject{Name: "Ana", Phone: strPtr("611222333")}
	after := diffSubject{Name: "Ana", Phone: strPtr("611222333")}

	if got := DiffFields(before, after, "Name", "Phone"); len(got) != 0 {
		t.Errorf("expected no differences, got %v", got)
	}
}

func TestDiffFieldsHandlesNilPointersGoingBothWays(t *testing.T) {
	cases := []struct {
		name          string
		before, after *string
		want          string
	}{
		{"cleared", strPtr("611222333"), nil, "Phone: 611222333 → (vacío)"},
		{"set", nil, strPtr("611222333"), "Phone: (vacío) → 611222333"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffFields(diffSubject{Phone: c.before}, diffSubject{Phone: c.after}, "Phone")
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("expected [%q], got %v", c.want, got)
			}
		})
	}
}

func TestDiffFieldsFormatsDatesAsPlainDates(t *testing.T) {
	before := diffSubject{Born: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	after := diffSubject{Born: time.Date(2001, 6, 15, 0, 0, 0, 0, time.UTC)}

	want := []string{"Born: 2000-01-01 → 2001-06-15"}
	if got := DiffFields(before, after, "Born"); len(got) != 1 || got[0] != want[0] {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestDiffFieldsFormatsNilOptionalDateAsEmpty(t *testing.T) {
	d := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	before := diffSubject{Until: nil}
	after := diffSubject{Until: &d}

	want := []string{"Until: (vacío) → 2024-03-01"}
	if got := DiffFields(before, after, "Until"); len(got) != 1 || got[0] != want[0] {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// TestDiffFieldsSkipsNilVersusEmptyStringOfTheSameField reproduces a
// spurious "Field: (vacío) → (vacío)" notification: a nil pointer and a
// pointer to "" are genuinely different by DeepEqual (exactly what a
// no-op self-edit could produce — a blank optional field arriving as an
// explicit null, saved over a value that was already blank but stored as
// "" rather than NULL), but they're indistinguishable to whoever reads
// the message, so neither direction should produce a line.
func TestDiffFieldsSkipsNilVersusEmptyStringOfTheSameField(t *testing.T) {
	cases := []struct {
		name          string
		before, after *string
	}{
		{"nil then empty", nil, strPtr("")},
		{"empty then nil", strPtr(""), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffFields(diffSubject{Phone: c.before}, diffSubject{Phone: c.after}, "Phone")
			if len(got) != 0 {
				t.Errorf("expected no differences, got %v", got)
			}
		})
	}
}

func TestDiffFieldsPanicsOnUnknownField(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected a panic for an unknown field name")
		}
	}()
	DiffFields(diffSubject{}, diffSubject{}, "DoesNotExist")
}

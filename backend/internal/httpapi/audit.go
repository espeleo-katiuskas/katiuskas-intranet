package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"
)

// NotifyAsync sends text to notifier on its own goroutine, with a
// bounded timeout and a context detached from the request (which may
// already be gone by the time the send completes) — the same
// fire-and-forget shape every Telegram notification in this app uses.
// notifier may be nil (Telegram not configured), in which case this is
// a no-op. Every package-specific mutation notification in this app (a
// person created, a membership edited, a loan returned...) goes
// through this instead of duplicating the goroutine/timeout dance at
// each call site.
func NotifyAsync(notifier MutationNotifier, text string) {
	if notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := notifier.Send(ctx, text); err != nil {
			slog.Error("notify telegram", "error", err)
		}
	}()
}

// DiffFields compares the named fields of before and after — two
// values of the same struct type — and returns one line per field that
// actually differs, formatted "name: before → after". It's what every
// PATCH-style mutation notification in this app uses to describe
// exactly what changed, without hand-writing a diff for every
// resource's own fields.
//
// The caller names only the fields that endpoint can actually change —
// typically matching its own UpdateInput — rather than every field on
// the struct: a derived/computed field that was never part of the
// edit (a computed role list, a membership date recalculated
// elsewhere) must never show up as a spurious difference just because
// it happened to be fetched at a slightly different moment than
// before.
//
// Panics if a named field doesn't exist on the struct — a programmer
// error (a typo in the field list), not something to handle at
// runtime.
func DiffFields(before, after any, fields ...string) []string {
	bv := reflect.ValueOf(before)
	av := reflect.ValueOf(after)
	var lines []string
	for _, name := range fields {
		bf := bv.FieldByName(name)
		af := av.FieldByName(name)
		if !bf.IsValid() || !af.IsValid() {
			panic(fmt.Sprintf("httpapi.DiffFields: no field %q on %s", name, bv.Type()))
		}
		if reflect.DeepEqual(bf.Interface(), af.Interface()) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s → %s", name, formatFieldValue(bf), formatFieldValue(af)))
	}
	return lines
}

// formatFieldValue renders a single field's value for DiffFields: a
// nil pointer (or zero time.Time) as "(vacío)", a pointer or
// non-pointer time.Time as a plain date (never a time — every date
// field this app diffs is a calendar date, not a timestamp), and
// anything else via its default fmt formatting once dereferenced.
func formatFieldValue(v reflect.Value) string {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "(vacío)"
		}
		v = v.Elem()
	}
	if t, ok := v.Interface().(time.Time); ok {
		if t.IsZero() {
			return "(vacío)"
		}
		return t.Format("2006-01-02")
	}
	s := fmt.Sprint(v.Interface())
	if s == "" {
		return "(vacío)"
	}
	return s
}

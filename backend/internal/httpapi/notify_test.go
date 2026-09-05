package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

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

func (f *fakeMutationNotifier) assertNothingSent(t *testing.T) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 0 {
		t.Errorf("expected no message sent, got %v", f.sent)
	}
}

func TestNotifyMutatingCallsReportsPostPutPatchDelete(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			notifier := &fakeMutationNotifier{}
			handler := NotifyMutatingCalls(notifier)(next)

			req := httptest.NewRequest(method, "/api/v1/users/ana.garcia", nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			msg := notifier.waitForMessage(t)
			want := "[API] " + method + " /api/v1/users/ana.garcia -> 201"
			if msg != want {
				t.Errorf("got %q, want %q", msg, want)
			}
		})
	}
}

func TestNotifyMutatingCallsIgnoresReadOnlyMethods(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			notifier := &fakeMutationNotifier{}
			handler := NotifyMutatingCalls(notifier)(next)

			req := httptest.NewRequest(method, "/api/v1/users", nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			notifier.assertNothingSent(t)
		})
	}
}

func TestNotifyMutatingCallsReportsFailedWritesToo(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	})
	notifier := &fakeMutationNotifier{}
	handler := NotifyMutatingCalls(notifier)(next)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/licenses", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	msg := notifier.waitForMessage(t)
	if msg != "[API] POST /api/v1/licenses -> 409" {
		t.Errorf("got %q", msg)
	}
}

func TestNotifyMutatingCallsWithNilNotifierIsANoOp(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})
	handler := NotifyMutatingCalls(nil)(next)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called || rec.Code != http.StatusCreated {
		t.Errorf("expected the request to still go through normally, called=%v status=%d", called, rec.Code)
	}
}

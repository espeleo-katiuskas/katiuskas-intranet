package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerPassesThroughStatusAndSetsRequestID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	RequestLogger(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("expected the wrapped handler's status to pass through, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Errorf("expected an X-Request-Id header to be set")
	}
}

// captureLog temporarily redirects the default slog logger to a buffer
// for the duration of fn, restoring the previous logger afterward.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(previous)
	fn()
	return buf.String()
}

func TestRequestLoggerIncludesUserIDWhenSet(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRequestUserID(r.Context(), 42)
	})

	logOutput := captureLog(t, func() {
		RequestLogger(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})

	if !strings.Contains(logOutput, "user_id=42") {
		t.Errorf("expected the log line to include user_id=42, got %q", logOutput)
	}
}

func TestRequestLoggerOmitsUserIDWhenNeverSet(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	logOutput := captureLog(t, func() {
		RequestLogger(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})

	if strings.Contains(logOutput, "user_id") {
		t.Errorf("expected no user_id in the log line for an unauthenticated request, got %q", logOutput)
	}
}

func TestSetRequestUserIDIsANoOpOutsideRequestLogger(t *testing.T) {
	// Calling it on a plain context (no requestLogFields present, e.g. a
	// request never wrapped by RequestLogger) must not panic.
	SetRequestUserID(httptest.NewRequest(http.MethodGet, "/", nil).Context(), 1)
}

func TestRequestLoggerDefaultsToOKWhenHandlerNeverWritesHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("no explicit WriteHeader call"))
	})

	rec := httptest.NewRecorder()
	RequestLogger(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rec.Code)
	}
}

func TestRecovererTurnsPanicIntoA500(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})

	rec := httptest.NewRecorder()
	Recoverer(panicking).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecovererLeavesNormalResponsesAlone(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	rec := httptest.NewRecorder()
	Recoverer(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201 to pass through untouched, got %d", rec.Code)
	}
}

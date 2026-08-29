package httpapi

import (
	"net/http"
	"net/http/httptest"
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

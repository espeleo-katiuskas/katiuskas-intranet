package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

type logFieldsKey struct{}

// requestLogFields is a per-request mutable box for extra fields the
// final "request" log line should include, filled in by middleware that
// runs *after* RequestLogger but knows something it doesn't — namely
// auth.RequireAuth, which resolves who's making the call. It works by
// reference: RequestLogger puts a pointer to this box in the context
// before dispatching, so a value set deep in the chain (on a request
// built from r.WithContext, which RequestLogger's own r variable never
// sees) is still visible once the chain returns, because it's the same
// underlying struct being mutated, not a replaced context value.
type requestLogFields struct {
	UserID *int64
}

// SetRequestUserID records the authenticated user's id for the request
// log line RequestLogger will write once the request finishes. A no-op
// outside a request wrapped by RequestLogger (there's nothing to set).
func SetRequestUserID(ctx context.Context, userID int64) {
	if fields, ok := ctx.Value(logFieldsKey{}).(*requestLogFields); ok {
		fields.UserID = &userID
	}
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-Id", requestID)

		fields := &requestLogFields{}
		r = r.WithContext(context.WithValue(r.Context(), logFieldsKey{}, fields))

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		attrs := []any{"request_id", requestID}
		if fields.UserID != nil {
			attrs = append(attrs, "user_id", *fields.UserID)
		}
		slog.Info(fmt.Sprintf("%s %s %d %dms", r.Method, r.URL.Path, rec.status,
			time.Since(start).Milliseconds()), attrs...)
	})
}

// Recoverer turns a panic in a handler into a 500 response instead of
// crashing the server.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "error", rec, "stack", string(debug.Stack()))
				WriteError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

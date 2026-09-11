package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPUsesLastForwardedForEntry(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.2")
	r.RemoteAddr = "10.0.0.2:54321"

	if got := ClientIP(r); got != "10.0.0.2" {
		t.Fatalf("expected the last entry (this API's own immediate proxy hop), got %q", got)
	}
}

func TestClientIPTrimsWhitespaceAroundLastEntry(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.9,  10.0.0.2 ")

	if got := ClientIP(r); got != "10.0.0.2" {
		t.Fatalf("expected surrounding whitespace trimmed, got %q", got)
	}
}

func TestClientIPSingleForwardedForEntry(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("expected the one entry present, got %q", got)
	}
}

func TestClientIPFallsBackToRemoteAddrWithoutForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:12345"

	if got := ClientIP(r); got != "192.0.2.1" {
		t.Fatalf("expected RemoteAddr's host, got %q", got)
	}
}

func TestClientIPFallsBackToRawRemoteAddrWithoutPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "not-a-host-port"

	if got := ClientIP(r); got != "not-a-host-port" {
		t.Fatalf("expected the raw RemoteAddr when it has no port, got %q", got)
	}
}

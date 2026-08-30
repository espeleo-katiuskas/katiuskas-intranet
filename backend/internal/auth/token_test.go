package auth

import "testing"

func TestNewOpaqueTokenIsUniqueAndNonEmpty(t *testing.T) {
	a, err := NewOpaqueToken()
	if err != nil {
		t.Fatalf("new opaque token: %v", err)
	}
	b, err := NewOpaqueToken()
	if err != nil {
		t.Fatalf("new opaque token: %v", err)
	}
	if a == "" || b == "" {
		t.Fatalf("expected non-empty tokens")
	}
	if a == b {
		t.Fatalf("expected two calls to produce different tokens, got the same one twice")
	}
}

func TestNewLoginCodeIsSixDigitsAndVaries(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		code, err := NewLoginCode()
		if err != nil {
			t.Fatalf("new login code: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("expected a 6-character code, got %q", code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("expected an all-digit code, got %q", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected repeated calls to vary, got the same code every time: %v", seen)
	}
}

func TestHashTokenIsDeterministicAndDistinct(t *testing.T) {
	if HashToken("same") != HashToken("same") {
		t.Errorf("expected hashing the same input twice to produce the same hash")
	}
	if HashToken("one") == HashToken("two") {
		t.Errorf("expected different inputs to produce different hashes")
	}
	if hash := HashToken("some-token"); hash == "some-token" {
		t.Errorf("expected the hash to differ from the raw input")
	}
}

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

func TestNewMagicLinkTokenIsUniqueAndShorterThanNewOpaqueToken(t *testing.T) {
	a, err := NewMagicLinkToken()
	if err != nil {
		t.Fatalf("new magic link token: %v", err)
	}
	b, err := NewMagicLinkToken()
	if err != nil {
		t.Fatalf("new magic link token: %v", err)
	}
	if a == "" || b == "" {
		t.Fatalf("expected non-empty tokens")
	}
	if a == b {
		t.Fatalf("expected two calls to produce different tokens, got the same one twice")
	}

	// 16 bytes (128 bits) versus NewOpaqueToken's 32 (256 bits) — half
	// the entropy, but still the widely-used baseline for this kind of
	// secret (see NewMagicLinkToken's own doc comment for why that's
	// still comfortably enough for a 15-minute-lived, URL-embedded
	// token). Checked via the encoded string's own length rather than a
	// hardcoded character count, so this doesn't silently stop meaning
	// anything if the encoding ever changes.
	full, err := NewOpaqueToken()
	if err != nil {
		t.Fatalf("new opaque token: %v", err)
	}
	if len(a) >= len(full) {
		t.Errorf("expected a magic link token to be shorter than a full opaque token, got %d vs %d chars", len(a), len(full))
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
	first, second := HashToken("same"), HashToken("same")
	if first != second {
		t.Errorf("expected hashing the same input twice to produce the same hash")
	}
	if HashToken("one") == HashToken("two") {
		t.Errorf("expected different inputs to produce different hashes")
	}
	if hash := HashToken("some-token"); hash == "some-token" {
		t.Errorf("expected the hash to differ from the raw input")
	}
}

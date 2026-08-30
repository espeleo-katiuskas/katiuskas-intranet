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

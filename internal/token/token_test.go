package token

import "testing"

func TestGenerateUniqueAndHashStable(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a == b {
		t.Fatal("two Generate calls returned the same token")
	}
	if a == "" {
		t.Fatal("empty token")
	}

	if Hash(a) != Hash(string([]byte(a))) {
		t.Error("Hash not stable for same input")
	}
	if Hash(a) == Hash(b) {
		t.Error("Hash collision for different tokens")
	}
	if len(Hash(a)) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(Hash(a)))
	}
}

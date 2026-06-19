package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestControlTokenLifecycle(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	if err := s.CreateControlToken(ctx, "id-1", "ci-bot", "hash-1", "deploy"); err != nil {
		t.Fatalf("CreateControlToken: %v", err)
	}

	name, role, ok, err := s.LookupControlToken(ctx, "hash-1")
	if err != nil {
		t.Fatalf("LookupControlToken: %v", err)
	}
	if !ok || name != "ci-bot" || role != "deploy" {
		t.Fatalf("lookup = (%q, %q, %v), want (ci-bot, deploy, true)", name, role, ok)
	}

	// Unknown hash → not found, no error.
	if _, _, ok, err := s.LookupControlToken(ctx, "nope"); err != nil || ok {
		t.Fatalf("lookup unknown = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Revoke → lookup no longer resolves.
	if err := s.RevokeControlToken(ctx, "id-1"); err != nil {
		t.Fatalf("RevokeControlToken: %v", err)
	}
	if _, _, ok, _ := s.LookupControlToken(ctx, "hash-1"); ok {
		t.Fatal("revoked token still resolves")
	}

	// Revoking again (or unknown) → ErrControlTokenNotFound.
	err = s.RevokeControlToken(ctx, "id-1")
	if !errors.As(err, new(ErrControlTokenNotFound)) {
		t.Fatalf("re-revoke err = %v, want ErrControlTokenNotFound", err)
	}
}

func TestListControlTokens(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	for _, tc := range []struct{ id, name, hash, role string }{
		{"id-1", "alice", "h1", "admin"},
		{"id-2", "ci", "h2", "read"},
	} {
		if err := s.CreateControlToken(ctx, tc.id, tc.name, tc.hash, tc.role); err != nil {
			t.Fatalf("create %s: %v", tc.name, err)
		}
	}
	if err := s.RevokeControlToken(ctx, "id-2"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	tokens, err := s.ListControlTokens(ctx)
	if err != nil {
		t.Fatalf("ListControlTokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("got %d tokens, want 2", len(tokens))
	}
	// List never exposes the hash; revoked entries carry RevokedAt.
	var sawRevoked bool
	for _, tk := range tokens {
		if tk.Name == "ci" {
			if tk.RevokedAt == nil {
				t.Errorf("ci token should be revoked")
			}
			sawRevoked = true
		}
	}
	if !sawRevoked {
		t.Error("revoked token missing from list")
	}
}

package ledger

import (
	"context"
	"testing"
)

func TestAgentTokens(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.CreateAgentToken(ctx, "id1", "web1", "hash1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	host, ok, err := store.AgentTokenHost(ctx, "hash1")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if host != "web1" {
		t.Errorf("host = %q, want web1", host)
	}

	// Unknown hash.
	if _, ok, _ := store.AgentTokenHost(ctx, "nope"); ok {
		t.Error("unknown hash reported as valid")
	}

	// Revocation invalidates lookup.
	if err := store.RevokeAgentToken(ctx, "id1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, ok, _ := store.AgentTokenHost(ctx, "hash1"); ok {
		t.Error("revoked token still valid")
	}

	list, err := store.ListAgentTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].RevokedAt == nil {
		t.Fatalf("list = %+v, want 1 revoked token", list)
	}
}

func TestCreateAgentToken_duplicateHash(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.CreateAgentToken(ctx, "id1", "web1", "dup"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.CreateAgentToken(ctx, "id2", "web1", "dup"); err == nil {
		t.Error("expected error on duplicate token_hash, got nil")
	}
}

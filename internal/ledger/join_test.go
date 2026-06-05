package ledger

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJoinTokenRedeemOnce(t *testing.T) {
	st := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	if err := st.CreateJoinToken(ctx, "id1", "web-1", "hashA", now.Add(15*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}

	host, err := st.RedeemJoinToken(ctx, "hashA", now)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if host != "web-1" {
		t.Fatalf("host = %q, want web-1", host)
	}

	// Second redeem of the same token must fail (single-use).
	if _, err := st.RedeemJoinToken(ctx, "hashA", now); !errors.Is(err, ErrJoinTokenInvalid) {
		t.Fatalf("second redeem err = %v, want ErrJoinTokenInvalid", err)
	}
}

func TestJoinTokenExpired(t *testing.T) {
	st := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	if err := st.CreateJoinToken(ctx, "id2", "web-2", "hashB", now.Add(-time.Second)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.RedeemJoinToken(ctx, "hashB", now); !errors.Is(err, ErrJoinTokenInvalid) {
		t.Fatalf("expired redeem err = %v, want ErrJoinTokenInvalid", err)
	}
}

func TestJoinTokenUnknown(t *testing.T) {
	st := openMemory(t)
	if _, err := st.RedeemJoinToken(context.Background(), "nope", time.Now()); !errors.Is(err, ErrJoinTokenInvalid) {
		t.Fatalf("unknown redeem err = %v, want ErrJoinTokenInvalid", err)
	}
}

func TestPurgeExpiredJoinTokens(t *testing.T) {
	st := openMemory(t)
	ctx := context.Background()
	now := time.Now()

	_ = st.CreateJoinToken(ctx, "live", "h", "hLive", now.Add(time.Hour))
	_ = st.CreateJoinToken(ctx, "dead", "h", "hDead", now.Add(-time.Hour))

	n, err := st.PurgeExpiredJoinTokens(ctx, now)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	// The live token still redeems.
	if _, err := st.RedeemJoinToken(ctx, "hLive", now); err != nil {
		t.Fatalf("live redeem after purge: %v", err)
	}
}

package ledger

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func openMemory(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRecordAndMarkStatus(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	r := DeployRecord{
		DeployID:    "01J000000000000000000001",
		Service:     "app",
		Host:        "web1",
		SHA:         "abc123",
		Status:      StatusPending,
		TriggeredBy: TriggeredByWebhook,
		StartedAt:   time.Now(),
	}
	if err := s.RecordDeploy(ctx, r); err != nil {
		t.Fatalf("RecordDeploy: %v", err)
	}
	if err := s.MarkStatus(ctx, r.DeployID, StatusSuccess); err != nil {
		t.Fatalf("MarkStatus: %v", err)
	}

	deploys, err := s.ListDeploys(ctx, "app", 10)
	if err != nil {
		t.Fatalf("ListDeploys: %v", err)
	}
	if len(deploys) != 1 {
		t.Fatalf("want 1, got %d", len(deploys))
	}
	if deploys[0].Status != StatusSuccess {
		t.Errorf("want success, got %s", deploys[0].Status)
	}
}

func TestRollbackTarget(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	shas := []string{"sha1", "sha2", "sha3"}
	for i, sha := range shas {
		id := fmt.Sprintf("id-%d", i)
		r := DeployRecord{
			DeployID:    id,
			Service:     "app",
			Host:        "web1",
			SHA:         sha,
			Status:      StatusPending,
			TriggeredBy: TriggeredByManual,
			StartedAt:   time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := s.RecordDeploy(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkStatus(ctx, id, StatusSuccess); err != nil {
			t.Fatal(err)
		}
	}

	// Latest = sha3. 1 step back = sha2, 2 steps back = sha1.
	target, err := s.RollbackTarget(ctx, "app", 1)
	if err != nil {
		t.Fatal(err)
	}
	if target != "sha2" {
		t.Errorf("want sha2, got %s", target)
	}

	target, err = s.RollbackTarget(ctx, "app", 2)
	if err != nil {
		t.Fatal(err)
	}
	if target != "sha1" {
		t.Errorf("want sha1, got %s", target)
	}
}

func TestRollbackTarget_notEnoughHistory(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	_, err := s.RollbackTarget(ctx, "app", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSeenNonce(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	ttl := 10 * time.Minute

	seen, err := s.SeenNonce(ctx, "nonce1", ttl)
	if err != nil || seen {
		t.Fatalf("first insert: seen=%v err=%v", seen, err)
	}
	seen, err = s.SeenNonce(ctx, "nonce1", ttl)
	if err != nil || !seen {
		t.Fatalf("repeat: should be seen=true, got %v err=%v", seen, err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			r := DeployRecord{
				DeployID:    fmt.Sprintf("id-%d", i),
				Service:     "app",
				Host:        "web1",
				SHA:         fmt.Sprintf("sha%d", i),
				Status:      StatusPending,
				TriggeredBy: TriggeredByManual,
				StartedAt:   time.Now(),
			}
			errs <- s.RecordDeploy(ctx, r)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent write error: %v", err)
		}
	}
}

package ledger

import (
	"context"
	"testing"
	"time"
)

func TestServiceLifecycle_volumePurge(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// A timed service and a manual service, both removed with containers down.
	if err := s.MarkServicePresent(ctx, "db", "web1", "7 days"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkServicePresent(ctx, "cache", "web1", "manual"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ServiceDeleteVolumes(ctx, "db"); got != "7 days" {
		t.Errorf("ServiceDeleteVolumes(db) = %q, want '7 days'", got)
	}

	past := now - 1000
	if err := s.MarkServiceRemoved(ctx, "db", &past); err != nil { // deadline already passed
		t.Fatal(err)
	}
	if err := s.MarkServiceRemoved(ctx, "cache", nil); err != nil { // manual: no deadline
		t.Fatal(err)
	}
	for _, svc := range []string{"db", "cache"} {
		if err := s.MarkContainersRemoved(ctx, svc); err != nil {
			t.Fatal(err)
		}
	}

	// Only the timed service whose deadline passed is due for purge.
	due, err := s.ServicesAwaitingPurge(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Service != "db" {
		t.Fatalf("ServicesAwaitingPurge = %+v, want [db]", due)
	}

	// Prune covers everything pending, including the manual service.
	pending, err := s.ServicesPendingVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("ServicesPendingVolumes = %+v, want db + cache", pending)
	}

	// After purging db, it drops out of both sets.
	if err := s.MarkVolumesPurged(ctx, "db"); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.ServicesAwaitingPurge(ctx, now); len(due) != 0 {
		t.Fatalf("awaiting purge after purge = %+v, want none", due)
	}
	if pending, _ := s.ServicesPendingVolumes(ctx); len(pending) != 1 || pending[0].Service != "cache" {
		t.Fatalf("pending after purge = %+v, want [cache]", pending)
	}
}

func TestServiceLifecycle(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Two present services.
	if err := s.MarkServicePresent(ctx, "app", "web1", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkServicePresent(ctx, "api", "web2", "manual"); err != nil {
		t.Fatal(err)
	}
	present, err := s.PresentServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(present) != 2 {
		t.Fatalf("present = %v, want 2 services", present)
	}

	// Remove api -> it should await teardown; app should not.
	if err := s.MarkServiceRemoved(ctx, "api", nil); err != nil {
		t.Fatal(err)
	}
	awaiting, err := s.ServicesAwaitingTeardown(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(awaiting) != 1 || awaiting[0].Service != "api" || awaiting[0].Host != "web2" {
		t.Fatalf("awaiting = %+v, want [api@web2]", awaiting)
	}

	// After the teardown is dispatched, api no longer awaits teardown.
	if err := s.MarkContainersRemoved(ctx, "api"); err != nil {
		t.Fatal(err)
	}
	awaiting, err = s.ServicesAwaitingTeardown(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(awaiting) != 0 {
		t.Fatalf("awaiting = %+v, want none after teardown", awaiting)
	}

	// Re-adding api resets its lifecycle: present again, not awaiting teardown.
	if err := s.MarkServicePresent(ctx, "api", "web2", "manual"); err != nil {
		t.Fatal(err)
	}
	present, err = s.PresentServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(present) != 2 {
		t.Fatalf("present after re-add = %v, want 2", present)
	}
	awaiting, _ = s.ServicesAwaitingTeardown(ctx)
	if len(awaiting) != 0 {
		t.Fatalf("awaiting after re-add = %+v, want none", awaiting)
	}
}

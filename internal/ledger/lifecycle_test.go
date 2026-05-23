package ledger

import (
	"context"
	"testing"
)

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

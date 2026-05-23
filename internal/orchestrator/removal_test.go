package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
)

// TestReconcileRemovals_detectsRemovedService drives the lifecycle through the
// syncer's removal logic directly (no git, no connected agent). With no agent
// connected, the teardown dispatch fails, so the service stays in the
// awaiting-teardown set to be retried — which is exactly what we assert.
func TestReconcileRemovals_detectsRemovedService(t *testing.T) {
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	syncer := NewGitSyncer("", "main", t.TempDir(), store, NewRegistry(), nil)
	ctx := context.Background()

	repoBoth := &config.Repo{Services: []config.Service{
		{Name: "app", Host: "web1"},
		{Name: "api", Host: "web2"},
	}}
	repoOnlyApp := &config.Repo{Services: []config.Service{
		{Name: "app", Host: "web1"},
	}}

	// Both present, nothing to tear down.
	syncer.reconcileRemovals(ctx, repoBoth)
	if awaiting, _ := store.ServicesAwaitingTeardown(ctx); len(awaiting) != 0 {
		t.Fatalf("awaiting = %+v, want none when both services present", awaiting)
	}

	// api leaves the repo: it becomes a removed service awaiting teardown.
	// (Send fails with no agent, so containers_removed_at stays unset.)
	syncer.reconcileRemovals(ctx, repoOnlyApp)
	awaiting, _ := store.ServicesAwaitingTeardown(ctx)
	if len(awaiting) != 1 || awaiting[0].Service != "api" || awaiting[0].Host != "web2" {
		t.Fatalf("awaiting = %+v, want [api@web2]", awaiting)
	}
	present, _ := store.PresentServices(ctx)
	if len(present) != 1 || present[0] != "app" {
		t.Fatalf("present = %v, want [app]", present)
	}

	// api comes back: lifecycle resets, nothing awaits teardown.
	syncer.reconcileRemovals(ctx, repoBoth)
	if awaiting, _ := store.ServicesAwaitingTeardown(ctx); len(awaiting) != 0 {
		t.Fatalf("awaiting after re-add = %+v, want none", awaiting)
	}
}

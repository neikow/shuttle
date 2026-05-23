package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
)

func TestPurgeAfterForPolicy(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	if got := purgeAfterForPolicy(config.DeleteVolumesManual, now); got != nil {
		t.Errorf("manual policy = %v, want nil (no scheduled purge)", *got)
	}
	if got := purgeAfterForPolicy("", now); got != nil {
		t.Errorf("empty policy = %v, want nil", *got)
	}
	if got := purgeAfterForPolicy(config.DeleteVolumesImmediate, now); got == nil || *got != now.UnixMilli() {
		t.Errorf("immediate policy = %v, want %d", got, now.UnixMilli())
	}
	if got := purgeAfterForPolicy("7 days", now); got == nil || *got != now.Add(7*24*time.Hour).UnixMilli() {
		t.Errorf("'7 days' policy = %v, want now+7d", got)
	}
	if got := purgeAfterForPolicy("nonsense", now); got != nil {
		t.Errorf("unparseable policy = %v, want nil (kept until prune)", *got)
	}
}

// TestPruneVolumes drives the prune set directly: a removed service whose volumes
// are pending is reported by PruneVolumes (no connected agent, so the dispatch
// fails and it stays pending — exactly the retry behavior we want).
func TestPruneVolumes_noAgentLeavesPending(t *testing.T) {
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// Removed service with containers down and a manual policy (pending prune).
	if err := store.MarkServicePresent(ctx, "db", "web1", config.DeleteVolumesManual); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkServiceRemoved(ctx, "db", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkContainersRemoved(ctx, "db"); err != nil {
		t.Fatal(err)
	}

	syncer := NewGitSyncer("", "main", t.TempDir(), store, NewRegistry(), nil)
	purged, err := syncer.PruneVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// No agent connected -> dispatch fails -> nothing marked purged.
	if len(purged) != 0 {
		t.Errorf("purged = %v, want none (no agent to dispatch to)", purged)
	}
	if pending, _ := store.ServicesPendingVolumes(ctx); len(pending) != 1 {
		t.Errorf("db should still be pending for retry, got %+v", pending)
	}
}

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

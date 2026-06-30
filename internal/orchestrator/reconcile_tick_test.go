package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

// syncedSyncer returns a GitSyncer pointed at a fresh file:// IaC repo with the
// working copy already synced, plus its ledger.
func syncedSyncer(t *testing.T) (*GitSyncer, *ledger.Store) {
	t.Helper()
	src := makeSourceRepo(t)
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	fake := secrets.NewFake(nil)
	fake.SetScope(secrets.Scope{Path: "/services/app"}, map[string]string{"API_KEY": "v"})
	g := NewGitSyncer("file://"+src, "main", t.TempDir(), store, NewRegistry(), fake)
	if _, err := g.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return g, store
}

func TestDriftReconciler_Tick(t *testing.T) {
	g, store := syncedSyncer(t)
	r := NewDriftReconciler(g, NewStateTracker(), time.Minute, time.Minute)
	r.SetEventBus(NewEventBus())
	r.tick(context.Background())

	// Reconcile against an empty ledger records a (pending) deploy for the repo's
	// service — proof the tick drove a real reconcile.
	deploys, err := store.ListDeploys(context.Background(), "app", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deploys) == 0 {
		t.Error("tick should have recorded a deploy for app")
	}
}

func TestDriftReconciler_RunReturnsOnCancel(t *testing.T) {
	g, _ := syncedSyncer(t)
	r := NewDriftReconciler(g, NewStateTracker(), time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled -> Run returns at the first select
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestSecretPoller_TickSeeds(t *testing.T) {
	g, _ := syncedSyncer(t)
	p := NewSecretPoller(g, time.Minute, "production")
	// First tick only seeds fingerprints (no redeploy, no error).
	p.tick(context.Background())
	// A second tick with unchanged secrets is a no-op too.
	p.tick(context.Background())
}

func TestBackupService_NoPolicyErrors(t *testing.T) {
	g, _ := syncedSyncer(t)
	g.SetBackupConfig(config.BackupConfig{})
	if _, _, err := g.BackupService(context.Background(), "app", ledger.TriggeredByManual); err == nil {
		t.Error("BackupService for a service with no backup policy should error")
	}
	// Unknown service also errors (exercises serviceFromWorkingRepo lookup).
	if _, _, err := g.BackupService(context.Background(), "nope", ledger.TriggeredByManual); err == nil {
		t.Error("BackupService for an unknown service should error")
	}
}

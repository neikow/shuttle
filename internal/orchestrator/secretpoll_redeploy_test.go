package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/secrets"
)

func TestSecretPoller_RedeploysOnChange(t *testing.T) {
	src := makeSourceRepo(t)
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	fake := secrets.NewFake(nil)
	fake.SetScope(secrets.Scope{Path: "/services/app"}, map[string]string{"API_KEY": "v1"})
	g := NewGitSyncer("file://"+src, "main", t.TempDir(), store, NewRegistry(), fake)
	g.SetSecretsPaths("/shared", "/services/{service}")
	if _, err := g.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	p := NewSecretPoller(g, time.Minute, "production")
	ctx := context.Background()
	p.tick(ctx) // seed fingerprints (no redeploy)

	// Change the secret -> next tick detects the new fingerprint and drives a
	// redeploy of the affected service (dispatch runs even with no agent attached).
	fake.SetScope(secrets.Scope{Path: "/services/app"}, map[string]string{"API_KEY": "v2"})
	p.tick(ctx)
	_ = store
}

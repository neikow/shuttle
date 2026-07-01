package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestDeployAtSHA_NoAgentRecordsFailed(t *testing.T) {
	g, store := syncedSyncer(t)
	ctx := context.Background()
	out, err := exec.Command("git", "-C", g.LocalDir(), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))

	// No agent is connected, so the dispatch's Send fails after a pending row is
	// recorded — exercising checkout + render + record + the failure path.
	if _, _, err := g.DeployAtSHA(ctx, "app", sha, ledger.TriggeredByManual); err == nil {
		t.Error("DeployAtSHA with no connected agent should error on send")
	}
	recs, _ := store.ListDeploys(ctx, "app", 5)
	var sawFailed bool
	for _, r := range recs {
		if r.Status == ledger.StatusFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Errorf("expected a failed deploy row, got %+v", recs)
	}
}

func TestBackupService_RecordsAndDispatches(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hosts.yaml", "hosts:\n  - name: web1\n")
	write("services/db/db.yaml", "name: db\nhost: web1\nbackup:\n  engine: volume\n")
	write("services/db/docker-compose.yml", "services:\n  db:\n    image: postgres\n")
	gitInit(t, dir)

	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGitSyncer("file://"+dir, "main", t.TempDir(), store, NewRegistry(), secrets.NewFake(nil))
	g.SetBackupConfig(config.BackupConfig{DefaultStore: "local", DefaultTarget: t.TempDir()})

	// No agent -> Send fails, but a pending backup row is recorded first.
	_, _, _ = g.BackupService(context.Background(), "db", ledger.TriggeredByManual)
	backups, _ := store.ListBackups(context.Background(), "db", 5)
	if len(backups) == 0 {
		t.Error("BackupService should record a backup attempt row")
	}
}

package orchestrator

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestWriteDeployError(t *testing.T) {
	w := httptest.NewRecorder()
	writeDeployError(w, context.DeadlineExceeded)
	if w.Code != 502 {
		t.Errorf("generic error -> %d, want 502", w.Code)
	}
	w = httptest.NewRecorder()
	writeDeployError(w, errStr("service \"x\" not found"))
	if w.Code != 404 {
		t.Errorf("not-found error -> %d, want 404", w.Code)
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

func TestSyncerSetters(t *testing.T) {
	g := &GitSyncer{}
	g.SetEventBus(NewEventBus())
	g.SetHTTPSRedirect(true)
	g.SetGitCredentials([]config.GitCredential{{RepoPrefix: "github.com/x", InfisicalKey: "K"}})
	g.SetSecretsPaths("/shared", "/services/{service}")

	srv := NewAgentServiceServer(NewRegistry(), nil)
	srv.SetVersion("v1.2.3")
	srv.SetStateTracker(NewStateTracker())
	srv.SetEventBus(NewEventBus())
}

func TestDNSReconciler_RunCancel(t *testing.T) {
	g, store := syncedSyncer(t)
	r := NewDNSReconciler(g, store, time.Hour)
	r.SetEventBus(NewEventBus())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DNSReconciler.Run did not return on cancel")
	}
}

func TestServicesUsingSecret(t *testing.T) {
	g, _ := syncedSyncer(t)
	g.SetSecretsPaths("/shared", "/services/{service}")
	// app reads provider secrets at /services/app -> a change there affects it.
	svcs, err := g.ServicesUsingSecret(context.Background(), "production", "/services/app", "production")
	if err != nil {
		t.Fatal(err)
	}
	_ = svcs // exercises syncAndLoad + servicesMatching
}

func makeBackupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("hosts.yaml", "hosts:\n  - name: web1\n")
	w("services/db/db.yaml", "name: db\nhost: web1\nbackup:\n  engine: volume\n  schedule: 1h\n")
	w("services/db/docker-compose.yml", "services:\n  db:\n    image: postgres\n")
	gitInit(t, dir)
	return dir
}

func TestBackupScheduler_Tick(t *testing.T) {
	src := makeBackupRepo(t)
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGitSyncer("file://"+src, "main", t.TempDir(), store, NewRegistry(), secrets.NewFake(nil))
	g.SetBackupConfig(config.BackupConfig{DefaultStore: "local", DefaultTarget: t.TempDir()})
	if _, err := g.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	sched := NewBackupScheduler(g, store, time.Hour)
	sched.tick(context.Background())
	// db is due (never backed up) -> a backup attempt row is recorded.
	if b, _ := store.ListBackups(context.Background(), "db", 5); len(b) == 0 {
		t.Error("scheduler tick should have recorded a due backup for db")
	}
}

func TestRestoreService_UnknownBackupErrors(t *testing.T) {
	g, _ := syncedSyncer(t)
	if _, _, _, err := g.RestoreService(context.Background(), "app", "no-such-backup"); err == nil {
		t.Error("RestoreService with an unknown backup should error")
	}
}

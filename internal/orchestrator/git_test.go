package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

// makeSourceRepo creates a minimal IaC git repo on disk and returns its path.
func makeSourceRepo(t *testing.T) string {
	t.Helper()
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
	write("services/app/app.yaml", "name: app\nhost: web1\nenv_schema:\n  - API_KEY\n")
	write("services/app/docker-compose.yml", "services:\n  app:\n    image: nginx\n")

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

func TestGitSyncer_Reconcile(t *testing.T) {
	src := makeSourceRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")

	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	registry := NewRegistry()
	conn := registry.register("web1") // simulate a connected agent

	sec := secrets.NewFake(map[string]string{"API_KEY": "s3cret", "UNUSED": "x"})
	g := NewGitSyncer(src, "main", clone, store, registry, sec)

	var caddyHits int32
	caddySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&caddyHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer caddySrv.Close()
	g.SetCaddy(NewCaddyClient(caddySrv.URL))

	dispatched, err := g.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(dispatched))
	}
	if atomic.LoadInt32(&caddyHits) == 0 {
		t.Error("expected Caddy ApplyRoutes to be called during reconcile")
	}

	// Inspect the command queued to the agent.
	select {
	case cmd := <-conn.send:
		dep := cmd.GetDeploy()
		if dep == nil {
			t.Fatal("expected deploy command")
		}
		if dep.Service != "app" {
			t.Fatalf("service = %q", dep.Service)
		}
		if len(dep.ComposeYaml) == 0 {
			t.Fatal("compose yaml empty")
		}
		if dep.Env["API_KEY"] != "s3cret" {
			t.Fatalf("env API_KEY = %q", dep.Env["API_KEY"])
		}
		if _, leaked := dep.Env["UNUSED"]; leaked {
			t.Fatal("env schema not enforced: UNUSED leaked")
		}
	default:
		t.Fatal("no command queued to agent")
	}

	// Mark deployed, then a second reconcile at the same SHA should be a no-op.
	if err := store.MarkStatus(context.Background(), dispatched[0], ledger.StatusSuccess); err != nil {
		t.Fatal(err)
	}
	again, err := g.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected no dispatch on unchanged SHA, got %d", len(again))
	}
}

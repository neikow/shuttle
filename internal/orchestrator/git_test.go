package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/neikow/shuttle/internal/config"
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
	write("services/app/app.yaml", "name: app\nhost: web1\nenv:\n  API_KEY: \"\"\n")
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

// gitInit initializes a repo at dir and commits its current contents.
func gitInit(t *testing.T, dir string) {
	t.Helper()
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
}

func TestGitSyncer_DeployAtSHA(t *testing.T) {
	src := makeSourceRepo(t)
	headCmd := exec.Command("git", "-C", src, "rev-parse", "HEAD")
	out, err := headCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))

	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := NewRegistry()
	conn := registry.register("web1", "")
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Path: "/shared"}, map[string]string{"API_KEY": "s3cret"})
	g := NewGitSyncer(src, "main", filepath.Join(t.TempDir(), "clone"), store, registry, sec)

	id, host, err := g.DeployAtSHA(context.Background(), "app", sha, ledger.TriggeredByManual)
	if err != nil {
		t.Fatalf("DeployAtSHA: %v", err)
	}
	if id == "" || host != "web1" {
		t.Fatalf("id=%q host=%q", id, host)
	}
	cmd := <-conn.send
	dep := cmd.GetDeploy()
	if dep == nil || dep.Service != "app" || dep.Sha != sha || len(dep.ComposeYaml) == 0 {
		t.Fatalf("unexpected deploy: %+v", dep)
	}

	// Ledger records it as a manual deploy.
	recs, err := store.ListDeploys(context.Background(), "app", 1)
	if err != nil || len(recs) != 1 || recs[0].TriggeredBy != ledger.TriggeredByManual {
		t.Fatalf("ledger: %v %+v", err, recs)
	}

	if _, _, err := g.DeployAtSHA(context.Background(), "ghost", sha, ledger.TriggeredByManual); err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestGitSyncer_PlanAndCheckRef(t *testing.T) {
	src := makeSourceRepo(t)
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A feature branch adds a second service; main keeps just "app".
	gitRun("checkout", "-q", "-b", "feature")
	write("services/extra/extra.yaml", "name: extra\nhost: web1\n")
	write("services/extra/docker-compose.yml", "services:\n  extra:\n    image: nginx\n")
	gitRun("add", "-A")
	gitRun("commit", "-q", "-m", "add extra")
	gitRun("checkout", "-q", "main")

	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	clone := filepath.Join(t.TempDir(), "clone")
	sec := secrets.NewFake(nil)
	sec.SetScope(secrets.Scope{Path: "/shared"}, map[string]string{"API_KEY": "s3cret"})
	g := NewGitSyncer(src, "main", clone, store, NewRegistry(), sec)

	// PlanRef against the branch sees both services (empty ledger → all create).
	rep, err := g.PlanRef(context.Background(), "feature")
	if err != nil {
		t.Fatalf("PlanRef(feature): %v", err)
	}
	got := map[string]PlanAction{}
	for _, e := range rep.Services {
		got[e.Service] = e.Action
	}
	if got["app"] != PlanCreate || got["extra"] != PlanCreate {
		t.Fatalf("feature plan = %+v, want app+extra create", rep.Services)
	}

	// Default (no ref) plans the configured branch HEAD: only "app".
	base, err := g.PlanRef(context.Background(), "")
	if err != nil {
		t.Fatalf("PlanRef(\"\"): %v", err)
	}
	if len(base.Services) != 1 || base.Services[0].Service != "app" {
		t.Fatalf("base plan = %+v, want only app", base.Services)
	}

	// CheckRef validates the branch's services from its isolated checkout.
	cr, err := g.CheckRef(context.Background(), "feature")
	if err != nil {
		t.Fatalf("CheckRef(feature): %v", err)
	}
	if len(cr.Services) != 2 || !cr.OK() {
		t.Fatalf("check report = %+v, ok=%v", cr.Services, cr.OK())
	}

	// The orchestrator's live working tree was never switched to the branch.
	if _, statErr := os.Stat(filepath.Join(clone, "services", "extra")); statErr == nil {
		t.Fatal("ref checkout leaked into the live working tree")
	}
}

func TestGitSyncer_RemoteCompose(t *testing.T) {
	// Remote repo holding the compose file.
	remote := t.TempDir()
	remoteCompose := "services:\n  api:\n    image: remote-image\n"
	if err := os.WriteFile(filepath.Join(remote, "stack.yml"), []byte(remoteCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, remote)

	// IaC repo with a service that points at the remote compose (no local file).
	src := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("hosts.yaml", "hosts:\n  - name: web1\n")
	mustWrite("services/api/api.yaml",
		"name: api\nhost: web1\nremote:\n  repo: "+remote+"\n  branch: main\n  path: stack.yml\n")
	gitInit(t, src)

	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := NewRegistry()
	conn := registry.register("web1", "")
	g := NewGitSyncer(src, "main", filepath.Join(t.TempDir(), "clone"), store, registry, nil)

	dispatched, err := g.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(dispatched))
	}
	cmd := <-conn.send
	if got := string(cmd.GetDeploy().ComposeYaml); got != remoteCompose {
		t.Fatalf("remote compose mismatch:\n got: %q\nwant: %q", got, remoteCompose)
	}
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
	conn := registry.register("web1", "") // simulate a connected agent

	sec := secrets.NewFake(nil)
	// Secrets live in the default base folder ("/shared"); renderEnv reads there.
	sec.SetScope(secrets.Scope{Path: "/shared"}, map[string]string{"API_KEY": "s3cret", "UNUSED": "x"})
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

func TestGitSyncer_credEnv(t *testing.T) {
	cred := config.GitCredential{
		RepoPrefix:   "github.com/myorg",
		InfisicalKey: "GITHUB_TOKEN",
	}

	tests := []struct {
		name    string
		syncer  func() *GitSyncer
		rawURL  string
		want    []string // nil => expect no credential env
		wantErr bool
	}{
		{
			name: "matching prefix injects scoped extraHeader env",
			syncer: func() *GitSyncer {
				sec := secrets.NewFake(map[string]string{"GITHUB_TOKEN": "tok123"})
				return &GitSyncer{secrets: sec, gitCreds: []config.GitCredential{cred}}
			},
			rawURL: "https://github.com/myorg/repo.git",
			// base64("oauth2:tok123") = b2F1dGgyOnRvazEyMw==
			want: []string{
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=http.https://github.com/myorg.extraHeader",
				"GIT_CONFIG_VALUE_0=Authorization: Basic b2F1dGgyOnRvazEyMw==",
			},
		},
		{
			name: "no matching prefix returns no credential env",
			syncer: func() *GitSyncer {
				sec := secrets.NewFake(map[string]string{"GITHUB_TOKEN": "tok123"})
				return &GitSyncer{secrets: sec, gitCreds: []config.GitCredential{cred}}
			},
			rawURL: "https://gitlab.com/other/repo.git",
			want:   nil,
		},
		{
			name: "nil secrets provider returns no credential env",
			syncer: func() *GitSyncer {
				return &GitSyncer{secrets: nil, gitCreds: []config.GitCredential{cred}}
			},
			rawURL: "https://github.com/myorg/repo.git",
			want:   nil,
		},
		{
			name: "secrets provider returns ErrNotFound",
			syncer: func() *GitSyncer {
				sec := secrets.NewFake(nil) // no keys seeded
				return &GitSyncer{secrets: sec, gitCreds: []config.GitCredential{cred}}
			},
			rawURL:  "https://github.com/myorg/repo.git",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.syncer()
			got, err := g.credEnv(context.Background(), tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var notFound secrets.ErrNotFound
				if !errors.As(err, &notFound) {
					t.Fatalf("expected ErrNotFound wrapped in error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("credEnv = %v, want %v", got, tt.want)
			}
			// The token must never leak into the remote URL or process args.
			for _, e := range got {
				if strings.Contains(e, "tok123") && !strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=") {
					t.Fatalf("token leaked outside the header value: %q", e)
				}
			}
		})
	}
}

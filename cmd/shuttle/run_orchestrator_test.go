package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

// TestRunOrchestrator_BootAndShutdown boots the full orchestrator (ledger, gRPC,
// HTTP control plane, git sync + reconcilers) against a local file:// repo, then
// cancels the context so it shuts down gracefully — covering the bootstrap and
// shutdown wiring without a real deployment.
func TestRunOrchestrator_BootAndShutdown(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(repo, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("hosts.yaml", "hosts:\n  - name: web1\n")
	w("services/app/app.yaml", "name: app\nhost: web1\n")
	w("services/app/docker-compose.yml", "services:\n  app:\n    image: nginx\n")
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "-A"}, {"commit", "-m", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	cfg := &config.OrchestratorConfig{
		BearerToken:     "secret",
		GRPCAddr:        "127.0.0.1:0",
		HTTPAddr:        "127.0.0.1:0",
		DataDir:         filepath.Join(t.TempDir(), "data"),
		RepoURL:         "file://" + repo,
		RepoBranch:      "main",
		WebhookSecret:   "whsecret",
		SecretsProvider: "none",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := runOrchestrator(ctx, cfg); err != nil {
		t.Fatalf("runOrchestrator: %v", err)
	}
}

func TestRunOrchestrator_TLSAndExtras(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(repo, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("hosts.yaml", "hosts:\n  - name: web1\n")
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "-A"}, {"commit", "-m", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	certDir := t.TempDir()
	crt := filepath.Join(certDir, "o.crt")
	key := filepath.Join(certDir, "o.key")
	if _, err := ensureSelfSignedCert(crt, key, []string{"localhost", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.OrchestratorConfig{
		BearerToken:            "secret",
		GRPCAddr:               "127.0.0.1:0",
		HTTPAddr:               "127.0.0.1:0",
		DataDir:                filepath.Join(t.TempDir(), "data"),
		RepoURL:                "file://" + repo,
		RepoBranch:             "main",
		WebhookSecret:          "whsecret",
		SecretsProvider:        "none",
		GRPCTLSCert:            crt,
		GRPCTLSKey:             key,
		AgentTokenAuth:         true,
		CaddyAdminURL:          "http://127.0.0.1:2019",
		InfisicalWebhookSecret: "isec",
		MetricsRequireAuth:     true,
		Notifications:          []config.NotificationTarget{{Type: "webhook", URL: "http://127.0.0.1:1/x"}},
		Backups:                config.BackupConfig{DefaultStore: "local", DefaultTarget: t.TempDir()},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := runOrchestrator(ctx, cfg); err != nil {
		t.Fatalf("runOrchestrator (tls+extras): %v", err)
	}
}

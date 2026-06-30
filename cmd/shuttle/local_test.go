package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func localRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(repo, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("hosts.yaml", "hosts:\n  - name: web1\n")
	w("services/app/app.yaml", "name: app\nhost: web1\n") // no env -> no secrets needed
	w("services/app/docker-compose.yml", "services:\n  app:\n    image: nginx\n")
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "-A"}, {"commit", "-m", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

func writeLocalConfig(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yml")
	body := "bearer_token: secret\ndata_dir: " + filepath.Join(dir, "data") +
		"\nrepo_url: file://" + repo + "\nrepo_branch: main\nsecrets_provider: none\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCheckLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cfg := writeLocalConfig(t, localRepo(t))
	if err := checkCmd.RunE(flagCmd(map[string]string{"config": cfg}), nil); err != nil {
		t.Errorf("check (local): %v", err)
	}
}

func TestPlanLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cfg := writeLocalConfig(t, localRepo(t))
	if err := planCmd.RunE(flagCmd(map[string]string{"config": cfg}), nil); err != nil {
		t.Errorf("plan (local): %v", err)
	}
}

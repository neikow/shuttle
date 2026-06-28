package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRepoOpts returns a minimal valid RepoInitOptions backed by a temp dir.
func makeRepoOpts(t *testing.T) RepoInitOptions {
	t.Helper()
	return RepoInitOptions{
		RepoDir:  t.TempDir(),
		RepoMode: "empty",
	}
}

// ── applyRepoInit: scaffold ──────────────────────────────────────────────────

func TestApplyRepoInit_ScaffoldsRepo(t *testing.T) {
	opts := makeRepoOpts(t)
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, filepath.Join(opts.RepoDir, ".gitignore"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "hosts.yaml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "services", ".gitkeep"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, ".git"))
}

func TestApplyRepoInit_GitignoreProtectsSecrets(t *testing.T) {
	opts := makeRepoOpts(t)
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.RepoDir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"config.yml", ".env", "certs/", "data/"} {
		assertContains(t, body, want)
	}
}

// ── orchestrator.yaml ────────────────────────────────────────────────────────

func TestApplyRepoInit_OrchestratorYAML_NoOverrides(t *testing.T) {
	opts := makeRepoOpts(t)
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	// With no overrides set, caddy + secrets paths are commented examples only.
	if strings.Contains(body, "\nsecrets_base_path:") {
		t.Error("orchestrator.yaml should not contain an active secrets_base_path with no override")
	}
	if strings.Contains(body, "\ncaddy_admin_url:") {
		t.Error("orchestrator.yaml should not contain an active caddy_admin_url with no override")
	}
	assertContains(t, body, "# caddy_admin_url:")
	assertContains(t, body, "# secrets_base_path:")
}

func TestApplyRepoInit_OrchestratorYAML_WithSecretsPaths(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.SecretsBasePath = "/shared"
	opts.SecretsPathTemplate = "/services/{service}"
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	assertContains(t, body, `secrets_base_path: "/shared"`)
	assertContains(t, body, `secrets_path_template: "/services/{service}"`)
}

func TestApplyRepoInit_OrchestratorYAML_WithCaddy(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.CaddyAdminURL = "http://caddy:2019"
	opts.HTTPSRedirect = true
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	assertContains(t, body, `caddy_admin_url: "http://caddy:2019"`)
	assertContains(t, body, `https_redirect: true`)
}

// ── CI provider ──────────────────────────────────────────────────────────────

func TestApplyRepoInit_GitHubCI(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.CIProvider = "github"
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, filepath.Join(opts.RepoDir, ".github", "workflows", "deploy.yml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, ".github", "workflows", "shuttle-plan.yml"))
}

func TestApplyRepoInit_GitLabCI(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.CIProvider = "gitlab"
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.RepoDir, ".gitlab-ci.yml")
	assertFileExists(t, path)
	data, _ := os.ReadFile(path)
	body := string(data)
	assertContains(t, body, "shuttle-deploy:")
	assertContains(t, body, "shuttle plan --url")
	// GitLab provider must not also write GitHub workflows.
	if _, err := os.Stat(filepath.Join(opts.RepoDir, ".github")); !errors.Is(err, os.ErrNotExist) {
		t.Error("gitlab provider should not write a .github dir")
	}
}

func TestApplyRepoInit_NoCIWhenNone(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.CIProvider = "none"
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.RepoDir, ".github")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected no .github dir when CIProvider=none")
	}
	if _, err := os.Stat(filepath.Join(opts.RepoDir, ".gitlab-ci.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected no .gitlab-ci.yml when CIProvider=none")
	}
}

func TestNormalizeCIProvider(t *testing.T) {
	cases := map[string]string{
		"github": "github", "GH": "github", "gitlab": "gitlab", "GL": "gitlab",
		"": "none", "bitbucket": "none", " none ": "none",
	}
	for in, want := range cases {
		if got := normalizeCIProvider(in); got != want {
			t.Errorf("normalizeCIProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── idempotency ──────────────────────────────────────────────────────────────

func TestApplyRepoInit_IdempotentRepo(t *testing.T) {
	opts := makeRepoOpts(t)
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	hostsPath := filepath.Join(opts.RepoDir, "hosts.yaml")
	custom := "hosts:\n  - name: custom\n"
	if err := os.WriteFile(hostsPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Error("second init run overwrote existing hosts.yaml")
	}
}

// ── starter mode ─────────────────────────────────────────────────────────────

func TestApplyRepoInit_StarterRepo_ScaffoldsWhoami(t *testing.T) {
	opts := makeRepoOpts(t)
	opts.RepoMode = "starter"
	if err := applyRepoInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, filepath.Join(opts.RepoDir, "services", "whoami", "whoami.yaml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "services", "whoami", "docker-compose.yml"))

	hosts, err := os.ReadFile(filepath.Join(opts.RepoDir, "hosts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(hosts), "name: local")

	if _, err := os.Stat(filepath.Join(opts.RepoDir, "services", ".gitkeep")); !errors.Is(err, os.ErrNotExist) {
		t.Error("starter repo should not contain services/.gitkeep")
	}
}

// ── prompt defaults ──────────────────────────────────────────────────────────

// TestPromptRepoInit_DefaultsStarter asserts hitting Enter (no --advanced) yields
// the starter repo with no CI/overrides — the short default path. Two Enters:
// repo mode + CI provider.
func TestPromptRepoInit_DefaultsStarter(t *testing.T) {
	dir := t.TempDir()
	opts, err := promptRepoInit(strings.NewReader("\n\n"), io.Discard, dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.RepoMode != "starter" {
		t.Errorf("RepoMode = %q, want starter", opts.RepoMode)
	}
	if opts.CIProvider != "none" {
		t.Errorf("CIProvider = %q, want none (default)", opts.CIProvider)
	}
	if opts.RepoDir != dir {
		t.Errorf("RepoDir = %q, want %q", opts.RepoDir, dir)
	}
}

func TestPromptRepoInit_EmptyChoiceAndGitLabCI(t *testing.T) {
	// "2" = empty repo, "3" = GitLab CI.
	opts, err := promptRepoInit(strings.NewReader("2\n3\n"), io.Discard, t.TempDir(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.RepoMode != "empty" {
		t.Errorf("RepoMode = %q, want empty", opts.RepoMode)
	}
	if opts.CIProvider != "gitlab" {
		t.Errorf("CIProvider = %q, want gitlab", opts.CIProvider)
	}
}

// TestPromptRepoInit_CIFlagSkipsPrompt asserts a non-empty --ci pre-answers the
// CI question, so only the repo-mode prompt consumes input.
func TestPromptRepoInit_CIFlagSkipsPrompt(t *testing.T) {
	opts, err := promptRepoInit(strings.NewReader("1\n"), io.Discard, t.TempDir(), "github", false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.CIProvider != "github" {
		t.Errorf("CIProvider = %q, want github (from --ci flag)", opts.CIProvider)
	}
}

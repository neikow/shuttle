package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestLoadRepoOrchestratorConfig_Absent(t *testing.T) {
	dir := t.TempDir()
	cfg, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for absent file")
	}
	if cfg != nil {
		t.Fatal("expected nil cfg for absent file")
	}
}

func TestLoadRepoOrchestratorConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	yaml := `
caddy_admin_url: "http://caddy:2019"
https_redirect: true
secrets_base_path: /shared
secrets_path_template: /services/{service}
git_credentials:
  - repo_prefix: github.com/myorg
    infisical_key: GITHUB_TOKEN
    infisical_env: production
    infisical_path: /shared
`
	write(t, dir, "orchestrator.yaml", yaml)

	cfg, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for present file")
	}
	if cfg.CaddyAdminURL != "http://caddy:2019" {
		t.Errorf("CaddyAdminURL = %q", cfg.CaddyAdminURL)
	}
	if cfg.HTTPSRedirect == nil || !*cfg.HTTPSRedirect {
		t.Error("HTTPSRedirect should be true")
	}
	if cfg.SecretsBasePath != "/shared" {
		t.Errorf("SecretsBasePath = %q", cfg.SecretsBasePath)
	}
	if cfg.SecretsPathTemplate != "/services/{service}" {
		t.Errorf("SecretsPathTemplate = %q", cfg.SecretsPathTemplate)
	}
	if len(cfg.GitCredentials) != 1 {
		t.Fatalf("GitCredentials len = %d", len(cfg.GitCredentials))
	}
	gc := cfg.GitCredentials[0]
	if gc.RepoPrefix != "github.com/myorg" {
		t.Errorf("RepoPrefix = %q", gc.RepoPrefix)
	}
	if gc.InfisicalKey != "GITHUB_TOKEN" {
		t.Errorf("InfisicalKey = %q", gc.InfisicalKey)
	}
}

func TestLoadRepoOrchestratorConfig_Partial(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "orchestrator.yaml", `caddy_admin_url: "http://caddy:2019"`)

	cfg, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cfg.CaddyAdminURL != "http://caddy:2019" {
		t.Errorf("CaddyAdminURL = %q", cfg.CaddyAdminURL)
	}
	if cfg.HTTPSRedirect != nil {
		t.Error("HTTPSRedirect should be nil when not set")
	}
	if cfg.SecretsBasePath != "" {
		t.Error("SecretsBasePath should be empty when not set")
	}
}

func TestLoadRepoOrchestratorConfig_HTTPSRedirectFalse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "orchestrator.yaml", `https_redirect: false`)

	cfg, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	// false is distinct from absent: pointer must be non-nil.
	if cfg.HTTPSRedirect == nil {
		t.Error("HTTPSRedirect should be non-nil when explicitly set to false")
	}
	if *cfg.HTTPSRedirect {
		t.Error("HTTPSRedirect should be false")
	}
}

func TestLoadRepoOrchestratorConfig_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "orchestrator.yaml", `unknown_key: value`)

	_, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if !ok {
		t.Fatal("expected ok=true for present-but-invalid file")
	}
	if err == nil {
		t.Fatal("expected error for unknown key (strict parsing)")
	}
}

func TestLoadRepoOrchestratorConfig_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "orchestrator.yaml", `caddy_admin_url: {unclosed`)

	_, ok, err := config.LoadRepoOrchestratorConfig(dir)
	if !ok {
		t.Fatal("expected ok=true for present file")
	}
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

// makeOpts returns a minimal valid InitOptions backed by temp directories.
func makeOpts(t *testing.T) InitOptions {
	t.Helper()
	return InitOptions{
		OutputDir:       t.TempDir(),
		RepoDir:         t.TempDir(),
		DataDir:         "./data",
		GRPCAddr:        ":9090",
		HTTPAddr:        ":8080",
		BearerToken:     "test-bearer",
		WebhookSecret:   "test-webhook",
		TLSMode:         "insecure",
		SecretsProvider: "none",
	}
}

// ── applyInit ────────────────────────────────────────────────────────────────

func TestApplyInit_WritesConfigYML(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	assertContains(t, body, `bearer_token: "test-bearer"`)
	assertContains(t, body, `webhook_secret: "test-webhook"`)
	assertContains(t, body, `grpc_addr: ":9090"`)
	assertContains(t, body, `http_addr: ":8080"`)
	assertContains(t, body, `data_dir: "./data"`)
	assertContains(t, body, `secrets_provider: "none"`)
}

func TestApplyInit_AutoGeneratesTokens(t *testing.T) {
	opts := makeOpts(t)
	opts.BearerToken = ""
	opts.WebhookSecret = ""
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "bearer_token:"); ok {
			val := strings.Trim(strings.TrimSpace(rest), `"`)
			if len(val) != 64 {
				t.Errorf("auto-generated bearer_token hex len = %d, want 64", len(val))
			}
		}
	}
}

func TestApplyInit_ConfigPerms(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.yml perm = %o, want 0600", perm)
	}
}

func TestApplyInit_NoDotEnvWhenSecretsNone(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected no .env when secrets_provider is none")
	}
}

func TestApplyInit_WritesDotEnvForInfisical(t *testing.T) {
	opts := makeOpts(t)
	opts.SecretsProvider = "infisical"
	opts.InfisicalClientID = "client-id"
	opts.InfisicalClientSecret = "client-secret"
	opts.InfisicalProjectID = "proj-id"
	opts.InfisicalEnv = "staging"
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	assertContains(t, body, "INFISICAL_CLIENT_ID=client-id")
	assertContains(t, body, "INFISICAL_CLIENT_SECRET=client-secret")
	assertContains(t, body, "INFISICAL_PROJECT_ID=proj-id")
	assertContains(t, body, "INFISICAL_ENV=staging")
}

func TestApplyInit_DotEnvPerms(t *testing.T) {
	opts := makeOpts(t)
	opts.SecretsProvider = "infisical"
	opts.InfisicalClientID = "id"
	opts.InfisicalClientSecret = "secret"
	opts.InfisicalProjectID = "proj"
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(opts.OutputDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".env perm = %o, want 0600", perm)
	}
}

func TestApplyInit_ScaffoldsRepo(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, filepath.Join(opts.RepoDir, ".gitignore"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "hosts.yaml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "services", ".gitkeep"))
	assertFileExists(t, filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, ".git"))
}

func TestApplyInit_OrchestratorYAML_NoSecrets(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.RepoDir, "orchestrator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secrets_base_path:") {
		t.Error("orchestrator.yaml should not contain secrets_base_path when provider=none")
	}
}

func TestApplyInit_OrchestratorYAML_WithInfisical(t *testing.T) {
	opts := makeOpts(t)
	opts.SecretsProvider = "infisical"
	opts.SecretsBasePath = "/shared"
	opts.SecretsPathTemplate = "/services/{service}"
	opts.InfisicalClientID = "id"
	opts.InfisicalClientSecret = "secret"
	opts.InfisicalProjectID = "proj"
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
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

func TestApplyInit_OrchestratorYAML_WithCaddy(t *testing.T) {
	opts := makeOpts(t)
	opts.CaddyAdminURL = "http://caddy:2019"
	opts.HTTPSRedirect = true
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
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

func TestApplyInit_GitHubActionsWorkflows(t *testing.T) {
	opts := makeOpts(t)
	opts.SetupGitHubActions = true
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, filepath.Join(opts.RepoDir, ".github", "workflows", "deploy.yml"))
	assertFileExists(t, filepath.Join(opts.RepoDir, ".github", "workflows", "shuttle-plan.yml"))
}

func TestApplyInit_NoGitHubActionsWhenDisabled(t *testing.T) {
	opts := makeOpts(t)
	opts.SetupGitHubActions = false
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.RepoDir, ".github")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected no .github dir when SetupGitHubActions=false")
	}
}

func TestApplyInit_TLSToken_WritesFields(t *testing.T) {
	opts := makeOpts(t)
	opts.TLSMode = "token"
	opts.TLSCertPath = "/etc/shuttle/orchestrator.crt"
	opts.TLSKeyPath = "/etc/shuttle/orchestrator.key"
	opts.AgentTokenAuth = true
	opts.AdvertiseAddr = "orch.example.com:9090"
	opts.AdvertiseServerName = "orchestrator"
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	assertContains(t, body, `grpc_tls_cert: "/etc/shuttle/orchestrator.crt"`)
	assertContains(t, body, `grpc_tls_key: "/etc/shuttle/orchestrator.key"`)
	assertContains(t, body, `agent_token_auth: true`)
	assertContains(t, body, `advertise_addr: "orch.example.com:9090"`)
}

func TestApplyInit_TLSmTLS_WritesCAField(t *testing.T) {
	opts := makeOpts(t)
	opts.TLSMode = "mtls"
	opts.TLSCertPath = "/etc/shuttle/orchestrator.crt"
	opts.TLSKeyPath = "/etc/shuttle/orchestrator.key"
	opts.TLSCAPath = "/etc/shuttle/ca.crt"
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `grpc_tls_ca: "/etc/shuttle/ca.crt"`)
}

func TestApplyInit_IdempotentRepo(t *testing.T) {
	opts := makeOpts(t)
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	hostsPath := filepath.Join(opts.RepoDir, "hosts.yaml")
	custom := "hosts:\n  - name: custom\n"
	if err := os.WriteFile(hostsPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second run must not overwrite existing files.
	if err := applyInit(context.Background(), opts, io.Discard); err != nil {
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

// ── generateHexToken ─────────────────────────────────────────────────────────

func TestGenerateHexToken_Length(t *testing.T) {
	tok := generateHexToken()
	if len(tok) != 64 {
		t.Errorf("token len = %d, want 64", len(tok))
	}
}

func TestGenerateHexToken_Unique(t *testing.T) {
	a, b := generateHexToken(), generateHexToken()
	if a == b {
		t.Error("two generated tokens should be different")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Errorf("expected to find %q in output", substr)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %s", path)
	}
}

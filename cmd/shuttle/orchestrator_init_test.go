package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeOrchOpts returns a minimal valid OrchInitOptions backed by a temp dir.
func makeOrchOpts(t *testing.T) OrchInitOptions {
	t.Helper()
	return OrchInitOptions{
		OutputDir:       t.TempDir(),
		DataDir:         "./data",
		GRPCAddr:        ":9090",
		HTTPAddr:        ":8080",
		BearerToken:     "test-bearer",
		WebhookSecret:   "test-webhook",
		RepoBranch:      "main",
		TLSMode:         "insecure",
		SecretsProvider: "none",
	}
}

// ── config.yml ───────────────────────────────────────────────────────────────

func TestApplyOrchInit_WritesConfigYML(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.RepoURL = "https://github.com/me/iac.git"
	if err := applyOrchInit(opts, io.Discard); err != nil {
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
	assertContains(t, body, `repo_url: "https://github.com/me/iac.git"`)
	assertContains(t, body, `repo_branch: "main"`)
}

func TestApplyOrchInit_AutoGeneratesTokens(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.BearerToken = ""
	opts.WebhookSecret = ""
	if err := applyOrchInit(opts, io.Discard); err != nil {
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

func TestApplyOrchInit_ConfigPerms(t *testing.T) {
	opts := makeOrchOpts(t)
	if err := applyOrchInit(opts, io.Discard); err != nil {
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

func TestApplyOrchInit_DefaultsRepoBranch(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.RepoBranch = ""
	if err := applyOrchInit(opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	assertContains(t, string(data), `repo_branch: "main"`)
}

// ── .env ─────────────────────────────────────────────────────────────────────

func TestApplyOrchInit_NoDotEnvWhenSecretsNone(t *testing.T) {
	opts := makeOrchOpts(t)
	if err := applyOrchInit(opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected no .env when secrets_provider is none")
	}
}

func TestApplyOrchInit_WritesDotEnvForInfisical(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.SecretsProvider = "infisical"
	opts.SecretsBasePath = "/shared"
	opts.SecretsPathTemplate = "/services/{service}"
	opts.InfisicalClientID = "client-id"
	opts.InfisicalClientSecret = "client-secret"
	opts.InfisicalProjectID = "proj-id"
	opts.InfisicalEnv = "staging"
	if err := applyOrchInit(opts, io.Discard); err != nil {
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

	// secrets paths land in config.yml for the infisical provider.
	cfg, _ := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	assertContains(t, string(cfg), `secrets_base_path: "/shared"`)
}

func TestApplyOrchInit_DotEnvPerms(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.SecretsProvider = "infisical"
	opts.InfisicalClientID = "id"
	opts.InfisicalClientSecret = "secret"
	opts.InfisicalProjectID = "proj"
	if err := applyOrchInit(opts, io.Discard); err != nil {
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

func TestApplyOrchInit_FileProvider(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.SecretsProvider = "file"
	if err := applyOrchInit(opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	assertContains(t, string(data), `secrets_provider: "file"`)
	if _, err := os.Stat(filepath.Join(opts.OutputDir, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Error("file provider should not write a .env")
	}
}

// ── TLS ──────────────────────────────────────────────────────────────────────

func TestApplyOrchInit_TLSToken_WritesFields(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.TLSMode = "token"
	opts.TLSCertPath = "/etc/shuttle/orchestrator.crt"
	opts.TLSKeyPath = "/etc/shuttle/orchestrator.key"
	opts.AgentTokenAuth = true
	opts.AdvertiseAddr = "orch.example.com:9090"
	opts.AdvertiseServerName = "orchestrator"
	if err := applyOrchInit(opts, io.Discard); err != nil {
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

func TestApplyOrchInit_TLSmTLS_WritesCAField(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.TLSMode = "mtls"
	opts.TLSCertPath = "/etc/shuttle/orchestrator.crt"
	opts.TLSKeyPath = "/etc/shuttle/orchestrator.key"
	opts.TLSCAPath = "/etc/shuttle/ca.crt"
	if err := applyOrchInit(opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(data), `grpc_tls_ca: "/etc/shuttle/ca.crt"`)
}

func TestApplyOrchInit_GeneratesSelfSignedCert(t *testing.T) {
	opts := makeOrchOpts(t)
	opts.TLSMode = "token"
	opts.AgentTokenAuth = true
	opts.GenerateCert = true
	opts.AdvertiseServerName = "orchestrator"
	opts.AdvertiseAddr = "localhost:9090"
	certDir := t.TempDir()
	opts.TLSCertPath = filepath.Join(certDir, "orchestrator.crt")
	opts.TLSKeyPath = filepath.Join(certDir, "orchestrator.key")
	if err := applyOrchInit(opts, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFileExists(t, opts.TLSCertPath)
	assertFileExists(t, opts.TLSKeyPath)

	info, err := os.Stat(opts.TLSKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key perm = %o, want 0600", perm)
	}

	pemBytes, err := os.ReadFile(opts.TLSCertPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("cert is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("orchestrator"); err != nil {
		t.Errorf("cert missing SAN orchestrator: %v", err)
	}
	if err := cert.VerifyHostname("localhost"); err != nil {
		t.Errorf("cert missing SAN localhost: %v", err)
	}
}

func TestEnsureSelfSignedCert_SkipsExisting(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "o.crt")
	keyPath := filepath.Join(dir, "o.key")
	if err := os.WriteFile(certPath, []byte("EXISTING"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("EXISTING"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := ensureSelfSignedCert(certPath, keyPath, []string{"orchestrator"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("should not regenerate when both files exist")
	}
	data, _ := os.ReadFile(certPath)
	if string(data) != "EXISTING" {
		t.Error("existing cert was overwritten")
	}
}

// ── secure defaults ──────────────────────────────────────────────────────────

// TestPromptOrchInit_SecureDefaults pumps an empty reader so every prompt takes
// its default (no --advanced), asserting that hitting Enter yields a secure
// setup: token enrollment over TLS, a cert to generate, an auto-generated
// (empty here) bearer token, and the default addresses/SAN.
func TestPromptOrchInit_SecureDefaults(t *testing.T) {
	opts, err := promptOrchInit(context.Background(), strings.NewReader(""), io.Discard, t.TempDir(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSMode != "token" {
		t.Errorf("TLSMode = %q, want token (secure default)", opts.TLSMode)
	}
	if !opts.AgentTokenAuth {
		t.Error("AgentTokenAuth should default true")
	}
	if !opts.GenerateCert {
		t.Error("GenerateCert should default true")
	}
	if opts.AdvertiseControlURL != "http://localhost:8080" {
		t.Errorf("AdvertiseControlURL = %q, want http://localhost:8080", opts.AdvertiseControlURL)
	}
	if opts.AdvertiseServerName != "orchestrator" {
		t.Errorf("AdvertiseServerName = %q, want orchestrator", opts.AdvertiseServerName)
	}
	if opts.BearerToken != "" {
		t.Error("bearer token should be left empty for auto-generation")
	}
	if opts.SecretsProvider != "none" {
		t.Errorf("SecretsProvider = %q, want none", opts.SecretsProvider)
	}
}

// ── repo_url detection ───────────────────────────────────────────────────────

func TestDetectRepoURL_FileURLForLocalRepo(t *testing.T) {
	dir := t.TempDir()
	// A scaffolded repo with no remote → file:// self-driving URL.
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte("hosts: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	got := detectRepoURL(context.Background(), dir)
	abs, _ := filepath.Abs(dir)
	if got != "file://"+abs {
		t.Errorf("detectRepoURL = %q, want file://%s", got, abs)
	}
}

func TestDetectRepoURL_ReusesOrigin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte("hosts: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/me/iac.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	if got := detectRepoURL(context.Background(), dir); got != "https://github.com/me/iac.git" {
		t.Errorf("detectRepoURL = %q, want the origin URL", got)
	}
}

func TestDetectRepoURL_NoRepo(t *testing.T) {
	if got := detectRepoURL(context.Background(), t.TempDir()); got != "" {
		t.Errorf("detectRepoURL = %q, want empty when no hosts.yaml present", got)
	}
}

// TestPromptOrchInit_RepoURLFlagWins asserts an explicit --repo-url is used
// verbatim and not overridden by detection.
func TestPromptOrchInit_RepoURLFlagWins(t *testing.T) {
	opts, err := promptOrchInit(context.Background(), strings.NewReader(""), io.Discard, t.TempDir(), "https://example.com/x.git", false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.RepoURL != "https://example.com/x.git" {
		t.Errorf("RepoURL = %q, want the flag value", opts.RepoURL)
	}
}

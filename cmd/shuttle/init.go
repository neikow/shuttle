package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a new orchestrator config and IaC repository",
	Long: `Interactive wizard that sets up a new Shuttle orchestrator environment.

It writes config.yml (bootstrap settings) and, if using Infisical, a .env
file (provider credentials). It also scaffolds the IaC git repository with
hosts.yaml, a services/ directory, and orchestrator.yaml — the repo-managed
settings that take effect on each reconcile without restarting the orchestrator.

Optionally adds GitHub Actions workflows for automated deploy-on-push and
PR plan-comment CI.`,
	Example: `  # Interactive setup in the current directory
  shuttle init

  # Write output to a specific directory
  shuttle init --dir /etc/shuttle`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		outputDir, _ := cmd.Flags().GetString("dir")
		opts, err := promptInitOptions(os.Stdin, os.Stdout, outputDir)
		if err != nil {
			return err
		}
		return applyInit(cmd.Context(), opts, os.Stdout)
	},
}

func init() {
	initCmd.Flags().String("dir", ".", "Directory to write config.yml and .env into")
}

// InitOptions holds every setting gathered during the init wizard.
// Separating prompt (I/O) from apply (logic) keeps applyInit fully testable.
type InitOptions struct {
	OutputDir string // where config.yml and .env are written
	RepoDir   string // where the IaC git repo is scaffolded

	// Bootstrap — written to config.yml (stay on the server, never in git).
	DataDir    string
	GRPCAddr   string
	HTTPAddr   string

	// Secrets written to config.yml; .env carries Infisical creds.
	BearerToken   string
	WebhookSecret string

	// IaC remote URL (repo_url in config.yml). Empty = fill in later.
	RemoteURL string

	// TLS: "insecure", "token", "mtls"
	TLSMode             string
	TLSCertPath         string
	TLSKeyPath          string
	TLSCAPath           string
	AdvertiseAddr       string
	AdvertiseServerName string
	AgentTokenAuth      bool

	// Secrets provider.
	SecretsProvider string // "none" or "infisical"

	// Caddy (written to orchestrator.yaml in the IaC repo).
	CaddyAdminURL string
	HTTPSRedirect bool

	// Repo-level config (orchestrator.yaml) extras.
	SecretsBasePath     string
	SecretsPathTemplate string

	// Infisical credentials — written to .env (only when provider=infisical).
	InfisicalClientID     string
	InfisicalClientSecret string
	InfisicalProjectID    string
	InfisicalEnv          string
	InfisicalSiteURL      string

	// GitHub Actions: whether to write workflow files into the IaC repo.
	SetupGitHubActions bool
}

// promptInitOptions runs the interactive wizard and returns a filled InitOptions.
func promptInitOptions(r io.Reader, w io.Writer, outputDir string) (InitOptions, error) {
	s := bufio.NewScanner(r)
	ask := func(prompt, defaultVal string) string {
		if defaultVal != "" {
			_, _ = fmt.Fprintf(w, "%s [%s]: ", prompt, defaultVal)
		} else {
			_, _ = fmt.Fprintf(w, "%s: ", prompt)
		}
		if !s.Scan() {
			return defaultVal
		}
		if v := strings.TrimSpace(s.Text()); v != "" {
			return v
		}
		return defaultVal
	}
	askBool := func(prompt string, def bool) bool {
		defStr := "y/N"
		if def {
			defStr = "Y/n"
		}
		v := strings.ToLower(ask(prompt+" ("+defStr+")", ""))
		switch v {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			return def
		}
	}
	askSecret := func(prompt string) string {
		_, _ = fmt.Fprintf(w, "%s (leave empty to auto-generate): ", prompt)
		if !s.Scan() {
			return ""
		}
		return strings.TrimSpace(s.Text())
	}

	_, _ = fmt.Fprintln(w, "\nShuttle init — sets up a new orchestrator environment.")
	_, _ = fmt.Fprintln(w)

	opts := InitOptions{OutputDir: outputDir}

	// ── Orchestrator settings ──────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "=== Orchestrator settings ===")
	opts.DataDir = ask("Data directory", "./data")
	opts.GRPCAddr = ask("gRPC address", ":9090")
	opts.HTTPAddr = ask("HTTP address", ":8080")

	// ── Security ──────────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== Security ===")
	opts.BearerToken = askSecret("Bearer token")
	opts.WebhookSecret = askSecret("Webhook secret")

	// ── IaC repository ────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== IaC repository ===")
	opts.RepoDir = ask("Local repo directory to scaffold", "./iac-repo")

	_, _ = fmt.Fprintln(w, "Remote URL options:")
	_, _ = fmt.Fprintln(w, "  1) I have an existing remote (enter URL)")
	_, _ = fmt.Fprintln(w, "  2) Skip for now (add remote later)")
	remoteChoice := ask("Choice", "2")
	if remoteChoice == "1" {
		opts.RemoteURL = ask("Remote URL", "")
	}

	opts.SetupGitHubActions = askBool("Set up GitHub Actions workflows (deploy + plan comment)?", false)

	// ── gRPC transport ────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== gRPC transport ===")
	_, _ = fmt.Fprintln(w, "  1) Insecure (dev only)")
	_, _ = fmt.Fprintln(w, "  2) Server TLS + token enrollment (recommended)")
	_, _ = fmt.Fprintln(w, "  3) Mutual TLS (mTLS)")
	tlsChoice := ask("Choice", "2")
	switch tlsChoice {
	case "3":
		opts.TLSMode = "mtls"
		opts.TLSCertPath = ask("TLS cert path", "/etc/shuttle/certs/orchestrator.crt")
		opts.TLSKeyPath = ask("TLS key path", "/etc/shuttle/certs/orchestrator.key")
		opts.TLSCAPath = ask("CA cert path", "/etc/shuttle/certs/ca.crt")
		opts.AgentTokenAuth = false
	case "2":
		opts.TLSMode = "token"
		opts.TLSCertPath = ask("TLS cert path", "/etc/shuttle/certs/orchestrator.crt")
		opts.TLSKeyPath = ask("TLS key path", "/etc/shuttle/certs/orchestrator.key")
		opts.AgentTokenAuth = true
		opts.AdvertiseAddr = ask("Advertise address (host:port agents dial)", "")
		opts.AdvertiseServerName = ask("Advertise server name (SAN on cert)", "orchestrator")
	default:
		opts.TLSMode = "insecure"
	}

	// ── Secrets ──────────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== Secrets provider ===")
	_, _ = fmt.Fprintln(w, "  1) None (no secret injection)")
	_, _ = fmt.Fprintln(w, "  2) Infisical")
	secretsChoice := ask("Choice", "1")
	if secretsChoice == "2" {
		opts.SecretsProvider = "infisical"
		opts.SecretsBasePath = ask("Shared secrets base path (absolute)", "/shared")
		opts.SecretsPathTemplate = ask("Per-service path template", "/services/{service}")
		_, _ = fmt.Fprintln(w, "\nInfisical credentials (written to .env, never to config.yml):")
		opts.InfisicalClientID = ask("INFISICAL_CLIENT_ID", "")
		opts.InfisicalClientSecret = ask("INFISICAL_CLIENT_SECRET", "")
		opts.InfisicalProjectID = ask("INFISICAL_PROJECT_ID", "")
		opts.InfisicalEnv = ask("INFISICAL_ENV", "production")
		opts.InfisicalSiteURL = ask("INFISICAL_SITE_URL (leave empty for cloud)", "")
	} else {
		opts.SecretsProvider = "none"
	}

	// ── Caddy ─────────────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== Caddy ingress (optional) ===")
	opts.CaddyAdminURL = ask("Caddy admin URL (empty to disable)", "")
	if opts.CaddyAdminURL != "" {
		opts.HTTPSRedirect = askBool("Enable HTTPS redirect (:443 only, 308-redirect :80)?", false)
	}

	return opts, s.Err()
}

// applyInit writes all generated files and scaffolds the IaC repo. It is
// separate from promptInitOptions so tests can call it with pre-filled options.
func applyInit(ctx context.Context, opts InitOptions, w io.Writer) error {
	// Auto-generate any missing secrets.
	if opts.BearerToken == "" {
		opts.BearerToken = generateHexToken()
	}
	if opts.WebhookSecret == "" {
		opts.WebhookSecret = generateHexToken()
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// ── config.yml ───────────────────────────────────────────────────────
	configPath := filepath.Join(opts.OutputDir, "config.yml")
	if err := writeConfigYML(configPath, opts); err != nil {
		return fmt.Errorf("write config.yml: %w", err)
	}
	_, _ = fmt.Fprintf(w, "Wrote %s\n", configPath)
	_, _ = fmt.Fprintf(w, "  ⚠ Protect this file: chmod 600 %s\n", configPath)

	// ── .env (Infisical creds) ────────────────────────────────────────────
	if opts.SecretsProvider == "infisical" {
		envPath := filepath.Join(opts.OutputDir, ".env")
		if err := writeDotEnv(envPath, opts); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		_, _ = fmt.Fprintf(w, "Wrote %s\n", envPath)
		_, _ = fmt.Fprintf(w, "  ⚠ Protect this file: chmod 600 %s\n", envPath)
	}

	// ── IaC repository ────────────────────────────────────────────────────
	if err := scaffoldRepo(ctx, opts, w); err != nil {
		return fmt.Errorf("scaffold IaC repo: %w", err)
	}

	// ── Next steps ────────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== Next steps ===")
	if opts.TLSMode != "insecure" {
		_, _ = fmt.Fprintln(w, "  1. Generate TLS certs (or provide your own):")
		_, _ = fmt.Fprintln(w, "       make certs")
	}
	step := 2
	if opts.TLSMode == "insecure" {
		step = 1
	}
	_, _ = fmt.Fprintf(w, "  %d. Add hosts to %s/hosts.yaml\n", step, opts.RepoDir)
	step++
	_, _ = fmt.Fprintf(w, "  %d. Add services under %s/services/\n", step, opts.RepoDir)
	step++
	if opts.RemoteURL == "" {
		_, _ = fmt.Fprintf(w, "  %d. Add a remote and push the IaC repo:\n", step)
		_, _ = fmt.Fprintf(w, "       cd %s && git remote add origin <url> && git push -u origin main\n", opts.RepoDir)
		step++
	}
	_, _ = fmt.Fprintf(w, "  %d. Start the orchestrator:\n", step)
	_, _ = fmt.Fprintf(w, "       shuttle orchestrator --config %s\n", configPath)
	step++
	_, _ = fmt.Fprintf(w, "  %d. Enroll your first host:\n", step)
	_, _ = fmt.Fprintf(w, "       shuttle enroll --url http://localhost%s --token '%s'\n", opts.HTTPAddr, opts.BearerToken)

	return nil
}

func writeConfigYML(path string, opts InitOptions) error {
	const tmpl = `# Shuttle orchestrator config — generated by shuttle init.
# Keep this file on the orchestrator server. Do not commit it to the IaC repo.

bearer_token: "{{ .BearerToken }}"
grpc_addr: "{{ .GRPCAddr }}"
http_addr: "{{ .HTTPAddr }}"
data_dir: "{{ .DataDir }}"

# IaC repository. Set both repo_url and webhook_secret to enable git sync and
# POST /webhook.
repo_url: "{{ .RemoteURL }}"
repo_branch: "main"
webhook_secret: "{{ .WebhookSecret }}"
{{ if eq .TLSMode "token" -}}
# gRPC — server TLS + token enrollment.
grpc_tls_cert: "{{ .TLSCertPath }}"
grpc_tls_key: "{{ .TLSKeyPath }}"
agent_token_auth: true
{{ if .AdvertiseAddr -}}
advertise_addr: "{{ .AdvertiseAddr }}"
{{ end -}}
{{ if .AdvertiseServerName -}}
advertise_server_name: "{{ .AdvertiseServerName }}"
{{ end -}}
{{ else if eq .TLSMode "mtls" -}}
# gRPC — mutual TLS.
grpc_tls_cert: "{{ .TLSCertPath }}"
grpc_tls_key: "{{ .TLSKeyPath }}"
grpc_tls_ca: "{{ .TLSCAPath }}"
{{ else -}}
# gRPC — insecure (dev only; add grpc_tls_cert/key for production).
{{ end -}}
secrets_provider: "{{ .SecretsProvider }}"
{{ if eq .SecretsProvider "infisical" -}}
secrets_base_path: "{{ .SecretsBasePath }}"
secrets_path_template: "{{ .SecretsPathTemplate }}"
{{ end -}}
`
	t := template.Must(template.New("config").Parse(tmpl))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return t.Execute(f, opts)
}

func writeDotEnv(path string, opts InitOptions) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	lines := []string{
		"# Infisical credentials for the Shuttle orchestrator.",
		"# Loaded automatically at startup from CWD/.env.",
		"# Keep this file protected and out of version control.",
	}
	add := func(k, v string) {
		if v != "" {
			lines = append(lines, k+"="+v)
		}
	}
	add("INFISICAL_CLIENT_ID", opts.InfisicalClientID)
	add("INFISICAL_CLIENT_SECRET", opts.InfisicalClientSecret)
	add("INFISICAL_PROJECT_ID", opts.InfisicalProjectID)
	add("INFISICAL_ENV", opts.InfisicalEnv)
	add("INFISICAL_SITE_URL", opts.InfisicalSiteURL)
	_, err = fmt.Fprintln(f, strings.Join(lines, "\n"))
	return err
}

// scaffoldRepo initialises a git repo at opts.RepoDir and writes the standard
// IaC scaffold: hosts.yaml, services/, orchestrator.yaml. If SetupGitHubActions
// is true it also writes the workflow files. Makes an initial commit.
func scaffoldRepo(ctx context.Context, opts InitOptions, w io.Writer) error {
	dir := opts.RepoDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// git init (idempotent if already a repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if out, err := exec.CommandContext(ctx, "git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(string(out)))
		}
		// Configure a sensible default branch name.
		_, _ = exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput()
	}

	// .gitignore
	if err := writeFileIfAbsent(filepath.Join(dir, ".gitignore"), gitignoreContent); err != nil {
		return err
	}

	// hosts.yaml
	if err := writeFileIfAbsent(filepath.Join(dir, "hosts.yaml"), hostsYAMLContent); err != nil {
		return err
	}

	// services/ with a .gitkeep so the directory is tracked.
	svcDir := filepath.Join(dir, "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		return err
	}
	if err := writeFileIfAbsent(filepath.Join(svcDir, ".gitkeep"), ""); err != nil {
		return err
	}

	// orchestrator.yaml
	orchYAML, err := renderOrchestratorYAML(opts)
	if err != nil {
		return err
	}
	if err := writeFileIfAbsent(filepath.Join(dir, "orchestrator.yaml"), orchYAML); err != nil {
		return err
	}

	// GitHub Actions workflows
	if opts.SetupGitHubActions {
		ghDir := filepath.Join(dir, ".github", "workflows")
		if err := os.MkdirAll(ghDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(ghDir, "deploy.yml"), deployWorkflowContent); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(ghDir, "shuttle-plan.yml"), planWorkflowContent); err != nil {
			return err
		}
	}

	// Set remote if provided.
	if opts.RemoteURL != "" {
		// Add or update origin.
		remotes, _ := exec.CommandContext(ctx, "git", "-C", dir, "remote").Output()
		if !strings.Contains(string(remotes), "origin") {
			if out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "add", "origin", opts.RemoteURL).CombinedOutput(); err != nil {
				return fmt.Errorf("git remote add: %w: %s", err, strings.TrimSpace(string(out)))
			}
		}
	}

	// Initial commit if the repo has no commits yet.
	out, _ := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if strings.Contains(string(out), "fatal") || strings.TrimSpace(string(out)) == "" {
		if _, err := exec.CommandContext(ctx, "git", "-C", dir, "add", ".").CombinedOutput(); err == nil {
			_, _ = exec.CommandContext(ctx, "git", "-C", dir,
				"-c", "user.email=shuttle@localhost",
				"-c", "user.name=Shuttle",
				"commit", "-q", "-m", "chore: shuttle init scaffold").CombinedOutput()
		}
	}

	_, _ = fmt.Fprintf(w, "Scaffolded IaC repo at %s\n", dir)
	return nil
}

func renderOrchestratorYAML(opts InitOptions) (string, error) {
	const tmpl = `# Orchestrator settings managed in git — no restart needed.
# Bootstrap settings (bearer_token, repo_url, webhook_secret, TLS, addresses)
# stay in config.yml on the orchestrator server.
#
# Changes here take effect on the next reconcile after pushing to the repo.

{{ if .CaddyAdminURL -}}
caddy_admin_url: "{{ .CaddyAdminURL }}"
https_redirect: {{ .HTTPSRedirect }}
{{ else -}}
# caddy_admin_url: "http://caddy:2019"
# https_redirect: false
{{ end -}}
{{ if eq .SecretsProvider "infisical" -}}
secrets_base_path: "{{ .SecretsBasePath }}"
secrets_path_template: "{{ .SecretsPathTemplate }}"

# Per-repo/org HTTPS credentials (token fetched from Infisical at runtime).
# git_credentials:
#   - repo_prefix: github.com/myorg
#     infisical_key: GITHUB_TOKEN
{{ end -}}
`
	t, err := template.New("orch").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, opts); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeFileIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func generateHexToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ── Static file content ────────────────────────────────────────────────────

const gitignoreContent = `# Shuttle runtime state (not repo config)
*.db
*.db-wal
*.db-shm
`

const hostsYAMLContent = `hosts:
  - name: web1
    labels:
      region: us-east
      role: edge
`

const deployWorkflowContent = `# Drop into .github/workflows/deploy.yml in your IaC repo.
# On push to main: signs a webhook payload and POSTs it to the orchestrator.
#
# Repo settings required:
#   Variable: SHUTTLE_URL            e.g. https://orchestrator.example.com:8080
#   Secret:   SHUTTLE_WEBHOOK_SECRET same value as webhook_secret in config.yml
name: Deploy via Shuttle

on:
  push:
    branches: [main]

jobs:
  notify:
    runs-on: ubuntu-latest
    steps:
      - name: Trigger Shuttle reconcile
        env:
          SHUTTLE_URL: ${{ vars.SHUTTLE_URL }}
          SHUTTLE_WEBHOOK_SECRET: ${{ secrets.SHUTTLE_WEBHOOK_SECRET }}
        run: |
          set -euo pipefail
          BODY=$(jq -nc \
            --arg ref "$GITHUB_REF" \
            --arg sha "$GITHUB_SHA" \
            --arg repo "$GITHUB_REPOSITORY" \
            '{ref:$ref, commit_sha:$sha, repo:$repo, services:[]}')
          TS=$(date +%s)
          SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SHUTTLE_WEBHOOK_SECRET" | awk '{print $NF}')"
          curl -fsS -X POST "$SHUTTLE_URL/webhook" \
            -H "X-Hub-Signature-256: $SIG" \
            -H "X-Shuttle-Timestamp: $TS" \
            -H "Content-Type: application/json" \
            --data-binary "$BODY"
`

const planWorkflowContent = `# Drop into .github/workflows/shuttle-plan.yml in your IaC repo.
# On every PR: validates the change and posts the orchestrator diff as a comment.
#
# Repo settings required:
#   Secret:   SHUTTLE_TOKEN  control-plane bearer token
#   Variable: SHUTTLE_URL    orchestrator control-plane URL
name: Shuttle plan

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - uses: neikow/shuttle/.github/actions/plan-comment@v1
        with:
          orchestrator-url: ${{ vars.SHUTTLE_URL }}
          token: ${{ secrets.SHUTTLE_TOKEN }}
          shuttle-version: latest
`

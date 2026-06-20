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
	DataDir  string
	GRPCAddr string
	HTTPAddr string

	// Secrets written to config.yml; .env carries Infisical creds.
	BearerToken   string
	WebhookSecret string

	// IaC remote URL (repo_url in config.yml). Empty = fill in later.
	RemoteURL string

	// RepoMode controls what the IaC repo is seeded with:
	//   "starter"  — an example whoami service + a "local" host to deploy first
	//   "empty"/"" — placeholder hosts.yaml + empty services/ (bring your own)
	//   "existing" — don't scaffold locally; just point repo_url at RemoteURL
	RepoMode string

	// TLS: "insecure", "token", "mtls". Defaults to "token" (server TLS + SSH-like
	// agent enrollment) — the recommended, secure-by-default transport.
	TLSMode             string
	TLSCertPath         string
	TLSKeyPath          string
	TLSCAPath           string
	AdvertiseAddr       string
	AdvertiseServerName string
	AgentTokenAuth      bool

	// AdvertiseControlURL is the externally reachable control-plane URL written to
	// config.yml so `shuttle enroll --config` needs no --url. Locally this is the
	// http_addr on localhost; in production it's the public HTTPS endpoint.
	AdvertiseControlURL string

	// GenerateCert, when true, makes applyInit write a fresh self-signed EC
	// orchestrator TLS cert/key at TLSCertPath/TLSKeyPath (skipped if they already
	// exist). This is what makes the secure token-enrollment path one step: agents
	// trust-on-first-use pin the cert and receive it via redeem, so there's no CA
	// to distribute and no openssl/make dependency.
	GenerateCert bool

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

	_, _ = fmt.Fprintln(w, "\nShuttle init — sets up a secure orchestrator environment.")
	_, _ = fmt.Fprintln(w, "Press Enter to accept the [default] for any question.")
	_, _ = fmt.Fprintln(w)

	opts := InitOptions{OutputDir: outputDir}

	// ── Orchestrator settings ──────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "=== Orchestrator ===")
	opts.DataDir = ask("Data directory", "./data")
	opts.HTTPAddr = ask("Control-plane HTTP address", ":8080")
	opts.GRPCAddr = ask("Agent gRPC address", ":9090")
	opts.AdvertiseControlURL = ask("Externally reachable control URL (agents/CI/enroll use it)", "http://localhost"+opts.HTTPAddr)

	// ── Secrets (auto-generated, protected) ────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== Control-plane secrets ===")
	_, _ = fmt.Fprintln(w, "The bearer token (admin auth) and webhook secret are auto-generated")
	_, _ = fmt.Fprintln(w, "with crypto/rand and written at mode 0600. Override only if you must.")
	opts.BearerToken = askSecret("Bearer token")
	opts.WebhookSecret = askSecret("Webhook secret")

	// ── Agent transport security ───────────────────────────────────────────
	// Token enrollment over TLS is the default: it encrypts + authenticates the
	// agent link with no per-agent certs and no inbound firewall holes.
	_, _ = fmt.Fprintln(w, "\n=== Agent transport security ===")
	_, _ = fmt.Fprintln(w, "  1) Token enrollment over TLS — recommended (SSH-like, no cert copying)")
	_, _ = fmt.Fprintln(w, "  2) Mutual TLS — advanced (you manage a CA + per-agent certs)")
	_, _ = fmt.Fprintln(w, "  3) Insecure — NO encryption or auth, local experiments only")
	switch ask("Choice", "1") {
	case "3":
		opts.TLSMode = "insecure"
		_, _ = fmt.Fprintln(w, "  ⚠ Insecure transport: the agent link is unencrypted and unauthenticated.")
	case "2":
		opts.TLSMode = "mtls"
		_, _ = fmt.Fprintln(w, "Run `make certs` (or your PKI) to produce the cert/key/CA, then point at them:")
		opts.TLSCertPath = ask("TLS cert path", "./certs/orchestrator.crt")
		opts.TLSKeyPath = ask("TLS key path", "./certs/orchestrator.key")
		opts.TLSCAPath = ask("CA cert path", "./certs/ca.crt")
		opts.AdvertiseAddr = ask("Advertise address (host:port agents dial)", "localhost"+opts.GRPCAddr)
		opts.AdvertiseServerName = ask("Cert hostname / SAN agents verify", "orchestrator")
	default:
		opts.TLSMode = "token"
		opts.AgentTokenAuth = true
		opts.TLSCertPath = ask("TLS cert path", "./certs/orchestrator.crt")
		opts.TLSKeyPath = ask("TLS key path", "./certs/orchestrator.key")
		opts.AdvertiseAddr = ask("Advertise address (host:port agents dial)", "localhost"+opts.GRPCAddr)
		opts.AdvertiseServerName = ask("Cert hostname / SAN agents verify", "orchestrator")
		// Generating the self-signed cert is what keeps "secure" and "easy"
		// together — agents pin it on first use and receive it via redeem.
		opts.GenerateCert = askBool("Generate a self-signed TLS cert now?", true)
	}

	// ── IaC repository ────────────────────────────────────────────────────
	_, _ = fmt.Fprintln(w, "\n=== IaC repository ===")
	_, _ = fmt.Fprintln(w, "  1) Starter repo with an example service (whoami) to deploy first")
	_, _ = fmt.Fprintln(w, "  2) Empty repo scaffold (bring your own services)")
	_, _ = fmt.Fprintln(w, "  3) Use an existing remote (enter URL, no local scaffold)")
	switch ask("Choice", "1") {
	case "3":
		opts.RepoMode = "existing"
		opts.RemoteURL = ask("Remote URL", "")
	case "2":
		opts.RepoMode = "empty"
		opts.RepoDir = ask("Local repo directory to scaffold", "./iac-repo")
		if askBool("Add an existing git remote (origin) now?", false) {
			opts.RemoteURL = ask("Remote URL", "")
		}
	default:
		opts.RepoMode = "starter"
		opts.RepoDir = ask("Local repo directory to scaffold", "./iac-repo")
		if askBool("Push the starter repo to an existing git remote (origin) now?", false) {
			opts.RemoteURL = ask("Remote URL", "")
		}
	}

	opts.SetupGitHubActions = askBool("Set up GitHub Actions workflows (deploy + plan comment)?", false)

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

	// A local starter repo with no remote drives itself: point repo_url at the
	// scaffolded repo via file:// so the orchestrator's git-sync loop deploys
	// the example service without a push or a remote.
	if opts.RepoMode == "starter" && opts.RemoteURL == "" {
		if abs, err := filepath.Abs(opts.RepoDir); err == nil {
			opts.RemoteURL = "file://" + abs
		}
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// ── TLS cert (self-signed, token path) ─────────────────────────────────
	// Generate the orchestrator's server cert so the secure default works with
	// no openssl/make and no CA to distribute. Skipped if the files already
	// exist so re-running init never clobbers a real cert.
	if opts.GenerateCert && opts.TLSCertPath != "" && opts.TLSKeyPath != "" {
		created, err := ensureSelfSignedCert(opts.TLSCertPath, opts.TLSKeyPath, certSANs(opts))
		if err != nil {
			return fmt.Errorf("generate TLS cert: %w", err)
		}
		if created {
			_, _ = fmt.Fprintf(w, "Generated self-signed TLS cert %s (key %s, mode 0600)\n", opts.TLSCertPath, opts.TLSKeyPath)
		} else {
			_, _ = fmt.Fprintf(w, "TLS cert %s already exists — left unchanged\n", opts.TLSCertPath)
		}
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
	// "existing" points repo_url at a remote the user already manages, so there
	// is nothing to scaffold locally.
	if opts.RepoMode != "existing" {
		if err := scaffoldRepo(ctx, opts, w); err != nil {
			return fmt.Errorf("scaffold IaC repo: %w", err)
		}
	}

	printNextSteps(w, opts, configPath)
	return nil
}

// printNextSteps writes the ordered, copy-pasteable commands to finish setup.
// It adapts to the transport (token enrollment vs insecure vs mtls) and to the
// repo mode, and — for the self-driving local starter — adds the whoami verify
// step so a first-timer sees a real deploy.
func printNextSteps(w io.Writer, opts InitOptions, configPath string) {
	starterLocal := opts.RepoMode == "starter" && strings.HasPrefix(opts.RemoteURL, "file://")
	insecure := opts.TLSMode == "insecure"

	_, _ = fmt.Fprintln(w, "\n=== Next steps ===")
	n := 0
	step := func(format string, a ...any) {
		n++
		_, _ = fmt.Fprintf(w, "  %d. "+format+"\n", append([]any{n}, a...)...)
	}

	if opts.TLSMode == "mtls" {
		step("Generate the cert/key/CA you pointed at (e.g. `make certs`).")
	}
	if opts.RepoMode == "empty" {
		step("Declare hosts in %s/hosts.yaml and services under %s/services/.", opts.RepoDir, opts.RepoDir)
	}
	if opts.RepoMode != "existing" && opts.RemoteURL != "" && !strings.HasPrefix(opts.RemoteURL, "file://") {
		step("Push the IaC repo to its remote:")
		_, _ = fmt.Fprintf(w, "       cd %s && git push -u origin main\n", opts.RepoDir)
	}

	step("Start the orchestrator:")
	_, _ = fmt.Fprintf(w, "       shuttle orchestrator --config %s\n", configPath)

	switch {
	case insecure:
		// No enrollment: the agent dials directly with no token.
		step("Start an agent (new terminal) for a declared host:")
		host := "<host>"
		if starterLocal {
			host = "local"
		}
		_, _ = fmt.Fprintf(w, "       shuttle agent --orchestrator localhost%s --host %s\n", opts.GRPCAddr, host)
	default:
		// Secure path: mint a single-use join token, then run join on the host.
		host := "<host>"
		if starterLocal {
			host = "local"
		}
		step("Enroll a host — prints a one-line `shuttle agent join …` command:")
		_, _ = fmt.Fprintf(w, "       shuttle enroll --config %s --host %s\n", configPath, host)
		if starterLocal {
			step("Run that printed command in a new terminal to start the local agent.")
		} else {
			step("Run that printed command once on the target host to start its agent.")
		}
	}

	if starterLocal {
		step("Watch the whoami example deploy (~60s), then verify it:")
		_, _ = fmt.Fprintln(w, "       curl localhost:8088")
		_, _ = fmt.Fprintf(w, "       curl -s -H \"Authorization: Bearer %s\" localhost%s/deploys | jq\n", opts.BearerToken, opts.HTTPAddr)
		_, _ = fmt.Fprintf(w, "\nEdit %s/services/whoami/ and commit to roll out a change.\n", opts.RepoDir)
	}
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
{{ if .AdvertiseControlURL -}}
# Externally reachable control-plane URL. 'shuttle enroll --config' reads it so
# enrolling a host needs no --url; in production make this the public HTTPS URL.
advertise_control_url: "{{ .AdvertiseControlURL }}"
{{ end -}}
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

	// hosts.yaml — the starter declares the "local" host its agent registers as;
	// otherwise a placeholder host to edit.
	hostsContent := hostsYAMLContent
	if opts.RepoMode == "starter" {
		hostsContent = starterHostsYAMLContent
	}
	if err := writeFileIfAbsent(filepath.Join(dir, "hosts.yaml"), hostsContent); err != nil {
		return err
	}

	// services/ — starter seeds a runnable whoami service; otherwise an empty
	// directory kept tracked with a .gitkeep.
	svcDir := filepath.Join(dir, "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		return err
	}
	if opts.RepoMode == "starter" {
		whoamiDir := filepath.Join(svcDir, "whoami")
		if err := os.MkdirAll(whoamiDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(whoamiDir, "whoami.yaml"), starterServiceYAMLContent); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(whoamiDir, "docker-compose.yml"), starterComposeYAMLContent); err != nil {
			return err
		}
	} else if err := writeFileIfAbsent(filepath.Join(svcDir, ".gitkeep"), ""); err != nil {
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

// starterHostsYAMLContent declares the single host the starter agent registers
// as (the host you enroll, or `shuttle agent --host local`).
const starterHostsYAMLContent = `hosts:
  - name: local
`

// starterServiceYAMLContent / starterComposeYAMLContent are the runnable whoami
// example — the first thing a new install deploys. recreate (not the rolling
// default) lets the compose file publish a fixed host port so it's reachable at
// http://localhost:8088 without Caddy. Replace it with your real services.
const starterServiceYAMLContent = `name: whoami
host: local
update_policy: recreate   # lets the example publish a fixed host port
`

const starterComposeYAMLContent = `services:
  whoami:
    image: traefik/whoami:latest
    ports: ["8088:80"]
    restart: unless-stopped
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

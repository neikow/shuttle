package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

var orchestratorInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate the orchestrator server config (config.yml, .env, TLS)",
	Long: `Generate the server-side bootstrap for an orchestrator: config.yml, an
optional .env (provider credentials), and self-signed TLS material under certs/.

This is the counterpart to 'shuttle init' (which scaffolds the git-managed IaC
repo). Run it in the same project directory and it auto-detects the scaffolded
repo to fill in repo_url: a local repo with no remote is driven directly over
file://, an existing 'origin' remote is reused. Override with --repo-url.

By default it asks two questions (agent transport + secrets provider) and takes
secure defaults for everything else: token enrollment over TLS, an auto-generated
bearer token + webhook secret, and a freshly generated self-signed cert. Pass
--advanced to be prompted for addresses, paths, advertise URLs, and SANs.`,
	Example: `  # Generate config.yml + certs in the current directory (after 'shuttle init')
  shuttle orchestrator init

  # Point at an existing IaC remote, prompt every setting
  shuttle orchestrator init --repo-url https://github.com/me/iac.git --advanced`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		advanced, _ := cmd.Flags().GetBool("advanced")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		opts, err := promptOrchInit(cmd.Context(), os.Stdin, os.Stdout, dir, repoURL, advanced)
		if err != nil {
			return err
		}
		return applyOrchInit(opts, os.Stdout)
	},
}

func init() {
	orchestratorInitCmd.Flags().String("dir", ".", "Project directory for config.yml, .env, and certs/")
	orchestratorInitCmd.Flags().Bool("advanced", false, "Prompt for advanced settings (addresses, paths, advertise URLs, SANs)")
	orchestratorInitCmd.Flags().String("repo-url", "", "IaC repo URL (overrides auto-detection from --dir)")
}

// OrchInitOptions holds every setting for generating the orchestrator's
// server-side bootstrap. Separating prompt (I/O) from apply (logic) keeps
// applyOrchInit fully testable.
type OrchInitOptions struct {
	OutputDir string // where config.yml, .env, and certs/ are written

	DataDir  string
	GRPCAddr string
	HTTPAddr string

	BearerToken   string
	WebhookSecret string

	RepoURL    string
	RepoBranch string

	// TLS: "insecure", "token", "mtls". Default "token" (server TLS + SSH-like
	// agent enrollment) — the recommended, secure-by-default transport.
	TLSMode             string
	TLSCertPath         string
	TLSKeyPath          string
	TLSCAPath           string
	AdvertiseAddr       string
	AdvertiseServerName string
	AgentTokenAuth      bool
	AdvertiseControlURL string

	// GenerateCert writes a fresh self-signed EC cert/key at TLSCertPath/
	// TLSKeyPath (skipped if they already exist).
	GenerateCert bool

	SecretsProvider     string // "none" | "infisical" | "file"
	SecretsBasePath     string
	SecretsPathTemplate string

	// Infisical credentials — written to .env (only when provider=infisical).
	InfisicalClientID     string
	InfisicalClientSecret string
	InfisicalProjectID    string
	InfisicalEnv          string
	InfisicalSiteURL      string
}

// promptOrchInit runs the (short by default) server-bootstrap wizard.
func promptOrchInit(ctx context.Context, r io.Reader, w io.Writer, dir, repoURL string, advanced bool) (OrchInitOptions, error) {
	p := newPrompter(r, w, advanced)
	p.line("\nShuttle orchestrator init — generate the server config.")
	if advanced {
		p.line("Advanced mode: every setting is prompted. Press Enter to accept a [default].")
	} else {
		p.line("Press Enter for the secure default. Re-run with --advanced for more options.")
	}
	p.line("")

	opts := OrchInitOptions{OutputDir: dir, RepoBranch: "main"}

	// ── Addresses (advanced; secure local defaults otherwise) ───────────────
	opts.DataDir = p.adv("Data directory", "./data")
	opts.HTTPAddr = p.adv("Control-plane HTTP address", ":8080")
	opts.GRPCAddr = p.adv("Agent gRPC address", ":9090")
	opts.AdvertiseControlURL = p.adv("Externally reachable control URL (agents/CI/enroll use it)", "http://localhost"+opts.HTTPAddr)

	// repo_url: flag wins, else auto-detect from the scaffolded repo in --dir.
	if repoURL == "" {
		repoURL = detectRepoURL(ctx, dir)
	}
	opts.RepoURL = p.adv("IaC repo URL", repoURL)
	opts.RepoBranch = p.adv("IaC repo branch", "main")

	// ── Control-plane secrets (advanced override; auto-generated otherwise) ──
	if advanced {
		p.line("\n=== Control-plane secrets ===")
		p.line("Leave empty to auto-generate with crypto/rand (written at mode 0600).")
		opts.BearerToken = p.askSecret("Bearer token")
		opts.WebhookSecret = p.askSecret("Webhook secret")
	}

	// ── Agent transport security (essential) ────────────────────────────────
	p.line("\n=== Agent transport security ===")
	p.line("  1) Token enrollment over TLS — recommended (SSH-like, no cert copying)")
	p.line("  2) Mutual TLS — advanced (you manage a CA + per-agent certs)")
	p.line("  3) Insecure — NO encryption or auth, local experiments only")
	switch p.ask("Choice", "1") {
	case "3":
		opts.TLSMode = "insecure"
		p.line("  ⚠ Insecure transport: the agent link is unencrypted and unauthenticated.")
	case "2":
		opts.TLSMode = "mtls"
		p.line("Run `make certs` (or your PKI) to produce the cert/key/CA, then point at them:")
		opts.TLSCertPath = p.ask("TLS cert path", "./certs/orchestrator.crt")
		opts.TLSKeyPath = p.ask("TLS key path", "./certs/orchestrator.key")
		opts.TLSCAPath = p.ask("CA cert path", "./certs/ca.crt")
		opts.AdvertiseAddr = p.ask("Advertise address (host:port agents dial)", "localhost"+opts.GRPCAddr)
		opts.AdvertiseServerName = p.ask("Cert hostname / SAN agents verify", "orchestrator")
	default:
		opts.TLSMode = "token"
		opts.AgentTokenAuth = true
		opts.TLSCertPath = p.adv("TLS cert path", "./certs/orchestrator.crt")
		opts.TLSKeyPath = p.adv("TLS key path", "./certs/orchestrator.key")
		opts.AdvertiseAddr = p.adv("Advertise address (host:port agents dial)", "localhost"+opts.GRPCAddr)
		opts.AdvertiseServerName = p.adv("Cert hostname / SAN agents verify", "orchestrator")
		// Generating the self-signed cert keeps "secure" and "easy" together —
		// agents pin it on first use and receive it via redeem.
		opts.GenerateCert = p.advBool("Generate a self-signed TLS cert now?", true)
	}

	// ── Secrets provider (essential) ────────────────────────────────────────
	p.line("\n=== Secrets provider ===")
	p.line("  1) None — no secret injection")
	p.line("  2) Infisical")
	p.line("  3) File — dotenv files under $SHUTTLE_SECRETS_DIR")
	switch p.ask("Choice", "1") {
	case "2":
		opts.SecretsProvider = "infisical"
		opts.SecretsBasePath = p.adv("Shared secrets base path (absolute)", "/shared")
		opts.SecretsPathTemplate = p.adv("Per-service path template", "/services/{service}")
		p.line("\nInfisical credentials (written to .env, never to config.yml):")
		opts.InfisicalClientID = p.ask("INFISICAL_CLIENT_ID", "")
		opts.InfisicalClientSecret = p.ask("INFISICAL_CLIENT_SECRET", "")
		opts.InfisicalProjectID = p.ask("INFISICAL_PROJECT_ID", "")
		opts.InfisicalEnv = p.ask("INFISICAL_ENV", "production")
		opts.InfisicalSiteURL = p.ask("INFISICAL_SITE_URL (leave empty for cloud)", "")
	case "3":
		opts.SecretsProvider = "file"
		p.line("  Secrets are read from $SHUTTLE_SECRETS_DIR/<env>/<path>.env at runtime.")
	default:
		opts.SecretsProvider = "none"
	}

	return opts, p.err()
}

// applyOrchInit writes config.yml, the optional .env, and (token mode) the
// self-signed TLS cert. Separate from promptOrchInit so tests can call it with
// pre-filled options.
func applyOrchInit(opts OrchInitOptions, w io.Writer) error {
	if opts.BearerToken == "" {
		opts.BearerToken = generateHexToken()
	}
	if opts.WebhookSecret == "" {
		opts.WebhookSecret = generateHexToken()
	}
	if opts.RepoBranch == "" {
		opts.RepoBranch = "main"
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// ── TLS cert (self-signed, token path) ─────────────────────────────────
	if opts.GenerateCert && opts.TLSCertPath != "" && opts.TLSKeyPath != "" {
		certPath := resolveUnderOutput(opts.OutputDir, opts.TLSCertPath)
		keyPath := resolveUnderOutput(opts.OutputDir, opts.TLSKeyPath)
		sans := certSANs(opts.AdvertiseServerName, opts.AdvertiseAddr, opts.AdvertiseControlURL)
		created, err := ensureSelfSignedCert(certPath, keyPath, sans)
		if err != nil {
			return fmt.Errorf("generate TLS cert: %w", err)
		}
		if created {
			_, _ = fmt.Fprintf(w, "Generated self-signed TLS cert %s (key %s, mode 0600)\n", certPath, keyPath)
		} else {
			_, _ = fmt.Fprintf(w, "TLS cert %s already exists — left unchanged\n", certPath)
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

	printOrchNextSteps(w, opts, configPath)
	return nil
}

// detectRepoURL fills in repo_url from a scaffolded IaC repo in dir: an existing
// "origin" remote is reused; otherwise a local repo (hosts.yaml present) is
// driven directly via file://. Returns "" when there's nothing to detect.
func detectRepoURL(ctx context.Context, dir string) string {
	if dir == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts.yaml")); err != nil {
		return "" // no scaffolded repo here
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output(); err == nil {
		if u := strings.TrimSpace(string(out)); u != "" {
			return u
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return "file://" + abs
	}
	return ""
}

// printOrchNextSteps writes the ordered, copy-pasteable commands to finish setup,
// adapting to the transport and whether a local starter repo is being driven.
func printOrchNextSteps(w io.Writer, opts OrchInitOptions, configPath string) {
	insecure := opts.TLSMode == "insecure"
	// A local starter repo (file:// repo_url + a scaffolded whoami service) can be
	// deployed and verified immediately, so we add the curl steps.
	starterLocal := strings.HasPrefix(opts.RepoURL, "file://")
	if starterLocal {
		if _, err := os.Stat(filepath.Join(opts.OutputDir, "services", "whoami", "whoami.yaml")); err != nil {
			starterLocal = false
		}
	}
	host := "<host>"
	if starterLocal {
		host = "local"
	}

	_, _ = fmt.Fprintln(w, "\n=== Next steps ===")
	n := 0
	step := func(format string, a ...any) {
		n++
		_, _ = fmt.Fprintf(w, "  %d. "+format+"\n", append([]any{n}, a...)...)
	}

	if opts.TLSMode == "mtls" {
		step("Generate the cert/key/CA you pointed at (e.g. `make certs`).")
	}

	step("Start the orchestrator:")
	_, _ = fmt.Fprintf(w, "       shuttle orchestrator --config %s\n", configPath)

	if insecure {
		step("Start an agent (new terminal) for a declared host:")
		_, _ = fmt.Fprintf(w, "       shuttle agent --orchestrator localhost%s --host %s\n", opts.GRPCAddr, host)
	} else {
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
	}
}

func writeConfigYML(path string, opts OrchInitOptions) error {
	const tmpl = `# Shuttle orchestrator config — generated by shuttle orchestrator init.
# Keep this file on the orchestrator server. Do not commit it to the IaC repo.

bearer_token: "{{ .BearerToken }}"
grpc_addr: "{{ .GRPCAddr }}"
http_addr: "{{ .HTTPAddr }}"
data_dir: "{{ .DataDir }}"

# IaC repository. Set both repo_url and webhook_secret to enable git sync and
# POST /webhook.
repo_url: "{{ .RepoURL }}"
repo_branch: "{{ .RepoBranch }}"
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

func writeDotEnv(path string, opts OrchInitOptions) error {
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

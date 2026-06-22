package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
	"github.com/spf13/cobra"
)

// doctorStatus is the outcome of a single preflight check.
type doctorStatus int

const (
	statusOK doctorStatus = iota
	statusWarn
	statusFail
)

func (s doctorStatus) glyph() string {
	switch s {
	case statusOK:
		return "✓"
	case statusWarn:
		return "!"
	default:
		return "✗"
	}
}

// doctorCheck is one line in the preflight report.
type doctorCheck struct {
	Name   string
	Status doctorStatus
	Detail string
}

// doctorOpts injects the side-effecting probes so buildDoctorReport is testable
// without touching the host (git/docker/clock). The CLI fills them with the real
// implementations in runDoctor.
type doctorOpts struct {
	now          time.Time
	certWarnDays int
	skipGit      bool
	skipDocker   bool

	lookPath    func(string) (string, error)
	gitLsRemote func(ctx context.Context, repoURL string) error
	dockerInfo  func(ctx context.Context) error
	newProvider func(name string) (secrets.Provider, error)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Preflight-check the orchestrator host: config, TLS, git, docker, secrets",
	Long: `Runs a set of host-level preflight checks before you start the orchestrator:

  • config.yml parses and is internally consistent
  • the git binary is present and the IaC repo is reachable (git ls-remote)
  • docker is reachable (only needed on agent hosts)
  • the data directory is writable (the SQLite ledger lives there)
  • the gRPC TLS material parses and is not expired (or expiring soon)
  • the configured secrets provider can be constructed

It dispatches nothing and writes no state. A failed check exits non-zero so it
fits a systemd ExecStartPre or a CI smoke test. For deep IaC-repo and
per-service secret validation, run 'shuttle check'.`,
	Example: `  shuttle doctor --config config.yml
  shuttle doctor --skip-docker   # on an orchestrator-only host`,
	RunE: runDoctor,
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	out := cmd.OutOrStdout()

	opts := doctorOpts{
		now:         time.Now(),
		lookPath:    exec.LookPath,
		gitLsRemote: gitLsRemoteProbe,
		dockerInfo:  dockerInfoProbe,
		newProvider: secrets.NewProvider,
	}
	opts.certWarnDays, _ = cmd.Flags().GetInt("cert-warn-days")
	opts.skipGit, _ = cmd.Flags().GetBool("skip-git")
	opts.skipDocker, _ = cmd.Flags().GetBool("skip-docker")

	var checks []doctorCheck
	cfg, err := config.LoadOrchestratorConfig(configPath)
	if err != nil {
		checks = append(checks, doctorCheck{"config", statusFail, err.Error()})
		renderDoctor(out, checks)
		return fmt.Errorf("doctor: configuration is invalid")
	}
	checks = append(checks, doctorCheck{"config", statusOK, configPath + " parses and is consistent"})
	checks = append(checks, buildDoctorReport(cmd.Context(), cfg, opts)...)

	renderDoctor(out, checks)
	if failed := countStatus(checks, statusFail); failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", failed)
	}
	if warned := countStatus(checks, statusWarn); warned > 0 {
		_, _ = fmt.Fprintf(out, "\n%d warning(s); no failures.\n", warned)
		return nil
	}
	_, _ = fmt.Fprintln(out, "\n✓ all checks passed")
	return nil
}

// buildDoctorReport runs every non-config check against cfg, using the injected
// probes. The config-parse check is prepended by the caller (it gates getting
// here at all).
func buildDoctorReport(ctx context.Context, cfg *config.OrchestratorConfig, opts doctorOpts) []doctorCheck {
	var checks []doctorCheck
	checks = append(checks, checkGit(ctx, cfg, opts)...)
	checks = append(checks, checkDocker(ctx, opts))
	checks = append(checks, checkDataDir(cfg))
	checks = append(checks, checkTransport(cfg, opts)...)
	checks = append(checks, checkSecrets(cfg, opts))
	return checks
}

// checkGit verifies the git binary is present and, when a repo is configured,
// that it is reachable. Reachability is a warning, not a failure: a private repo
// needs credentials the orchestrator injects at runtime (git_credentials) but
// doctor does not, so an auth failure here is expected and non-fatal.
func checkGit(ctx context.Context, cfg *config.OrchestratorConfig, opts doctorOpts) []doctorCheck {
	if opts.skipGit {
		return nil
	}
	if _, err := opts.lookPath("git"); err != nil {
		return []doctorCheck{{"git binary", statusFail, "git not found in PATH (required for IaC repo sync)"}}
	}
	checks := []doctorCheck{{"git binary", statusOK, "found"}}

	if cfg.RepoURL == "" {
		return append(checks, doctorCheck{"repo reachable", statusWarn, "repo_url not set; git sync disabled"})
	}
	if err := opts.gitLsRemote(ctx, cfg.RepoURL); err != nil {
		return append(checks, doctorCheck{"repo reachable", statusWarn,
			fmt.Sprintf("git ls-remote %s failed (private repos need git_credentials at runtime): %v", cfg.RepoURL, err)})
	}
	return append(checks, doctorCheck{"repo reachable", statusOK, cfg.RepoURL})
}

// checkDocker probes the local Docker daemon. Docker is only required on agent
// hosts, so its absence is a warning the operator can ignore on an
// orchestrator-only box (or with --skip-docker).
func checkDocker(ctx context.Context, opts doctorOpts) doctorCheck {
	if opts.skipDocker {
		return doctorCheck{"docker", statusOK, "skipped"}
	}
	if _, err := opts.lookPath("docker"); err != nil {
		return doctorCheck{"docker", statusWarn, "docker not found in PATH (required on agent hosts only)"}
	}
	if err := opts.dockerInfo(ctx); err != nil {
		return doctorCheck{"docker", statusWarn, fmt.Sprintf("docker found but daemon unreachable: %v", err)}
	}
	return doctorCheck{"docker", statusOK, "daemon reachable"}
}

// checkDataDir verifies the ledger directory is writable (the orchestrator
// MkdirAll's it at boot; doctor probes write access without leaving anything).
func checkDataDir(cfg *config.OrchestratorConfig) doctorCheck {
	dir := cfg.DataDir
	if dir == "" {
		dir = "./data" // mirrors the orchestrator command's --data-dir default
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return doctorCheck{"data dir", statusFail, fmt.Sprintf("%s: %v", dir, err)}
	}
	f, err := os.CreateTemp(dir, ".shuttle-doctor-*")
	if err != nil {
		return doctorCheck{"data dir", statusFail, fmt.Sprintf("%s not writable: %v", dir, err)}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return doctorCheck{"data dir", statusOK, dir + " writable"}
}

// checkTransport inspects the gRPC transport security: the TLS cert/key parse
// and the cert is not expired (or expiring within the warn window), and flags an
// insecure transport (token auth over cleartext, or no auth at all).
func checkTransport(cfg *config.OrchestratorConfig, opts doctorOpts) []doctorCheck {
	var checks []doctorCheck
	switch {
	case cfg.MTLSEnabled():
		checks = append(checks, doctorCheck{"grpc transport", statusOK, "mutual TLS"})
		checks = append(checks, inspectCertFile(cfg.GRPCTLSCert, opts))
		checks = append(checks, checkFileReadable("grpc_tls_key", cfg.GRPCTLSKey))
		checks = append(checks, checkFileReadable("grpc_tls_ca", cfg.GRPCTLSCA))
	case cfg.ServerTLSEnabled():
		checks = append(checks, doctorCheck{"grpc transport", statusOK, "server TLS (agents authenticate by token)"})
		checks = append(checks, inspectCertFile(cfg.GRPCTLSCert, opts))
		checks = append(checks, checkFileReadable("grpc_tls_key", cfg.GRPCTLSKey))
		if !cfg.AgentTokenAuth {
			checks = append(checks, doctorCheck{"agent auth", statusWarn, "server TLS without agent_token_auth: any client may register"})
		}
	default:
		if cfg.AgentTokenAuth {
			checks = append(checks, doctorCheck{"grpc transport", statusWarn, "agent_token_auth over cleartext gRPC; set grpc_tls_cert/key"})
		} else {
			checks = append(checks, doctorCheck{"grpc transport", statusWarn, "insecure: no TLS and no agent_token_auth; set grpc_tls_cert/key (+ agent_token_auth) or grpc_tls_ca"})
		}
	}
	return checks
}

// inspectCertFile parses a PEM cert and checks its validity window against the
// clock. An expired cert fails; one expiring within the warn window warns.
func inspectCertFile(path string, opts doctorOpts) doctorCheck {
	name := "tls cert"
	data, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{name, statusFail, fmt.Sprintf("%s: %v", path, err)}
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return doctorCheck{name, statusFail, fmt.Sprintf("%s: no PEM block found", path)}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return doctorCheck{name, statusFail, fmt.Sprintf("%s: parse: %v", path, err)}
	}
	switch {
	case opts.now.After(cert.NotAfter):
		return doctorCheck{name, statusFail, fmt.Sprintf("expired %s (NotAfter %s)", path, cert.NotAfter.Format(time.RFC3339))}
	case opts.now.Before(cert.NotBefore):
		return doctorCheck{name, statusFail, fmt.Sprintf("not yet valid %s (NotBefore %s)", path, cert.NotBefore.Format(time.RFC3339))}
	}
	if warn := opts.now.AddDate(0, 0, opts.certWarnDays); warn.After(cert.NotAfter) {
		days := int(cert.NotAfter.Sub(opts.now).Hours() / 24)
		return doctorCheck{name, statusWarn, fmt.Sprintf("%s expires in %d day(s) (%s)", path, days, cert.NotAfter.Format(time.RFC3339))}
	}
	return doctorCheck{name, statusOK, fmt.Sprintf("valid until %s", cert.NotAfter.Format(time.RFC3339))}
}

func checkFileReadable(name, path string) doctorCheck {
	if _, err := os.Stat(path); err != nil {
		return doctorCheck{name, statusFail, fmt.Sprintf("%s: %v", path, err)}
	}
	return doctorCheck{name, statusOK, path}
}

// checkSecrets constructs the configured secrets provider. Construction is where
// a provider validates its required environment (Infisical client creds, the
// file provider's SHUTTLE_SECRETS_DIR), so a clean build means deploys can at
// least reach the provider.
func checkSecrets(cfg *config.OrchestratorConfig, opts doctorOpts) doctorCheck {
	prov, err := opts.newProvider(cfg.SecretsProvider)
	if err != nil {
		return doctorCheck{"secrets provider", statusFail, fmt.Sprintf("%s: %v", cfg.SecretsProvider, err)}
	}
	if prov == nil {
		return doctorCheck{"secrets provider", statusOK, "none configured (services may only use literals / ${env:})"}
	}
	return doctorCheck{"secrets provider", statusOK, cfg.SecretsProvider + " constructed"}
}

func renderDoctor(out io.Writer, checks []doctorCheck) {
	for _, c := range checks {
		_, _ = fmt.Fprintf(out, "%s %-16s %s\n", c.Status.glyph(), c.Name, c.Detail)
	}
}

func countStatus(checks []doctorCheck, s doctorStatus) int {
	n := 0
	for _, c := range checks {
		if c.Status == s {
			n++
		}
	}
	return n
}

func gitLsRemoteProbe(ctx context.Context, repoURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", repoURL)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerInfoProbe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func init() {
	doctorCmd.Flags().String("config", "config.yml", "Path to the orchestrator config file")
	doctorCmd.Flags().Int("cert-warn-days", 30, "Warn when the gRPC TLS cert expires within this many days")
	doctorCmd.Flags().Bool("skip-git", false, "Skip the git binary + repo-reachability checks")
	doctorCmd.Flags().Bool("skip-docker", false, "Skip the docker daemon check (orchestrator-only hosts)")
}

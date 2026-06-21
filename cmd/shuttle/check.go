package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/neikow/shuttle/internal/secrets"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate the orchestrator config, the IaC repo, and secret availability",
	Long: `Validates a deployment without touching any agent or ledger:

  1. Syncs the IaC repo and validates its hosts/services (referential integrity).
  2. For every service with an env_schema, checks that each declared key is
     present in the configured secrets provider.

It dispatches no deploys and writes no state, so it is safe to run against a
live system (e.g. in CI before merging an IaC change). Exits non-zero if any
check fails.

Two modes:

  • Remote (--url + --token): asks a running orchestrator to validate against its
    own config + secrets provider. CI needs no local config.yml.
  • Local (--config): loads the config and validates the repo it points at.`,
	Example: `  # Against a running orchestrator (no local config needed)
  shuttle check --url https://orchestrator:8080 --token $SHUTTLE_TOKEN

  # Locally from a config file
  shuttle check --config config.yml`,
	RunE: runCheck,
}

func runCheck(cmd *cobra.Command, _ []string) error {
	url, _ := cmd.Flags().GetString("url")

	var report *orchestrator.CheckReport
	var err error
	if url != "" {
		report, err = checkRemote(cmd, url)
	} else {
		report, err = checkLocal(cmd)
	}
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	renderCheck(out, report)
	if !report.OK() {
		return fmt.Errorf("check failed")
	}
	_, _ = fmt.Fprintln(out, "✓ all checks passed")
	return nil
}

func checkRemote(cmd *cobra.Command, baseURL string) (*orchestrator.CheckReport, error) {
	bearer, _ := cmd.Flags().GetString("token")
	if bearer == "" {
		return nil, fmt.Errorf("--token is required with --url")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	endpoint := baseURL + "/check"
	if ref, _ := cmd.Flags().GetString("ref"); ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	body, _, err := doJSON(cmd.Context(), client, http.MethodGet, endpoint, bearer, nil)
	if err != nil {
		return nil, err
	}
	var report orchestrator.CheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("decode check: %w", err)
	}
	return &report, nil
}

func checkLocal(cmd *cobra.Command) (*orchestrator.CheckReport, error) {
	configPath, _ := cmd.Flags().GetString("config")
	out := cmd.OutOrStdout()

	cfg, err := config.LoadOrchestratorConfig(configPath)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(out, "✓ config %s is valid\n", configPath)

	if cfg.RepoURL == "" {
		return nil, fmt.Errorf("repo_url is not set; nothing to check beyond config.yml")
	}

	repoDir, _ := cmd.Flags().GetString("repo-dir")
	cleanup := func() {}
	if repoDir == "" {
		tmp, mkErr := os.MkdirTemp("", "shuttle-check-")
		if mkErr != nil {
			return nil, fmt.Errorf("create temp repo dir: %w", mkErr)
		}
		repoDir = tmp
		cleanup = func() { _ = os.RemoveAll(tmp) }
	}
	defer cleanup()

	secProvider, err := secrets.NewProvider(cfg.SecretsProvider)
	if err != nil {
		return nil, fmt.Errorf("secrets provider: %w", err)
	}

	// Check never dispatches or records, so the syncer needs no ledger/registry.
	syncer := orchestrator.NewGitSyncer(cfg.RepoURL, cfg.RepoBranch, repoDir, nil, nil, secProvider)
	syncer.SetSecretsPaths(cfg.SecretsBasePath, cfg.SecretsPathTemplate)
	syncer.SetGitCredentials(cfg.GitCredentials)
	return syncer.Check(cmd.Context())
}

func renderCheck(out io.Writer, report *orchestrator.CheckReport) {
	_, _ = fmt.Fprintf(out, "✓ repo synced at %s\n", shortSHA(report.SHA))

	if len(report.GitCredentials) > 0 {
		_, _ = fmt.Fprintf(out, "checking %d git credential(s):\n", len(report.GitCredentials))
		for _, gc := range report.GitCredentials {
			if gc.Err != "" {
				_, _ = fmt.Fprintf(out, "  ✗ %s (key=%s): %s\n", gc.RepoPrefix, gc.Key, gc.Err)
			} else {
				_, _ = fmt.Fprintf(out, "  ✓ %s (key=%s): token present\n", gc.RepoPrefix, gc.Key)
			}
		}
	}

	if len(report.DNSProviders) > 0 {
		_, _ = fmt.Fprintf(out, "checking %d DNS provider(s):\n", len(report.DNSProviders))
		for _, dp := range report.DNSProviders {
			if dp.Err != "" {
				_, _ = fmt.Fprintf(out, "  ✗ %s (%s): %s\n", dp.Provider, dp.Type, dp.Err)
			} else {
				_, _ = fmt.Fprintf(out, "  ✓ %s (%s): credentials present\n", dp.Provider, dp.Type)
			}
		}
	}

	if !report.HasProvider {
		_, _ = fmt.Fprintf(out, "! secrets provider not configured; skipping secret checks\n")
	}
	_, _ = fmt.Fprintf(out, "checking %d service(s):\n", len(report.Services))
	for _, sc := range report.Services {
		printServiceCheck(out, sc, report.HasProvider)
	}
}

func printServiceCheck(out io.Writer, sc orchestrator.ServiceCheck, hasProvider bool) {
	switch {
	case sc.Err != "":
		_, _ = fmt.Fprintf(out, "  ✗ %s: %s\n", sc.Service, sc.Err)
	case len(sc.MissingKeys) > 0:
		_, _ = fmt.Fprintf(out, "  ✗ %s (env=%s, %s + %s): missing %d key(s): %s\n",
			sc.Service, sc.Env, sc.BasePath, sc.ServicePath,
			len(sc.MissingKeys), strings.Join(sc.MissingKeys, ", "))
	case !hasProvider || len(sc.Schema) == 0:
		_, _ = fmt.Fprintf(out, "  ✓ %s: no secret checks (no env_schema or provider)\n", sc.Service)
	default:
		_, _ = fmt.Fprintf(out, "  ✓ %s (env=%s, %s + %s): all %d env_schema key(s) present\n",
			sc.Service, sc.Env, sc.BasePath, sc.ServicePath, len(sc.Schema))
	}
	for _, w := range sc.Warnings {
		_, _ = fmt.Fprintf(out, "  ! %s: %s\n", sc.Service, w)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func init() {
	checkCmd.Flags().String("url", "", "Orchestrator control-plane URL for a remote check, e.g. https://orchestrator:8080")
	checkCmd.Flags().String("token", "", "Control-plane bearer token (required with --url)")
	checkCmd.Flags().String("ref", "", "Git ref to check instead of the orchestrator's branch HEAD (remote mode; branch, tag, refs/pull/N/head, or SHA)")
	checkCmd.Flags().String("config", "config.yml", "Path to orchestrator config file (local mode)")
	checkCmd.Flags().String("repo-dir", "", "Existing IaC repo checkout to validate (local mode; default: clone into a temp dir)")
}

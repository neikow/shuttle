package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/neikow/shuttle/internal/secrets"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate the orchestrator config, the IaC repo, and secret availability",
	Long: `Validates a deployment without touching any agent or ledger:

  1. Loads and validates the orchestrator config.yml.
  2. Syncs the IaC repo and validates its hosts/services (referential integrity).
  3. For every service with an env_schema, checks that each declared key is
     present in the configured secrets provider.

It dispatches no deploys and writes no state, so it is safe to run against a
live system (e.g. in CI before merging an IaC change). Exits non-zero if any
check fails.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		out := cmd.OutOrStdout()

		cfg, err := config.LoadOrchestratorConfig(configPath)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "✓ config %s is valid\n", configPath)

		if cfg.RepoURL == "" {
			return fmt.Errorf("repo_url is not set; nothing to check beyond config.yml")
		}

		repoDir, _ := cmd.Flags().GetString("repo-dir")
		cleanup := func() {}
		if repoDir == "" {
			tmp, mkErr := os.MkdirTemp("", "shuttle-check-")
			if mkErr != nil {
				return fmt.Errorf("create temp repo dir: %w", mkErr)
			}
			repoDir = tmp
			cleanup = func() { _ = os.RemoveAll(tmp) }
		}
		defer cleanup()

		secProvider, err := secrets.NewProvider(cfg.SecretsProvider)
		if err != nil {
			return fmt.Errorf("secrets provider: %w", err)
		}

		// Check never dispatches or records, so the syncer needs no ledger/registry.
		syncer := orchestrator.NewGitSyncer(cfg.RepoURL, cfg.RepoBranch, repoDir, nil, nil, secProvider)
		syncer.SetSecretsPaths(cfg.SecretsBasePath, cfg.SecretsPathTemplate)

		report, err := syncer.Check(cmd.Context())
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "✓ repo %s synced at %s\n", cfg.RepoURL, shortSHA(report.SHA))

		if secProvider == nil {
			_, _ = fmt.Fprintf(out, "! secrets provider not configured; skipping secret checks\n")
		}
		_, _ = fmt.Fprintf(out, "checking %d service(s):\n", len(report.Services))
		for _, sc := range report.Services {
			printServiceCheck(out, sc, secProvider != nil)
		}

		if !report.OK() {
			return fmt.Errorf("check failed")
		}
		_, _ = fmt.Fprintln(out, "✓ all checks passed")
		return nil
	},
}

func printServiceCheck(out io.Writer, sc orchestrator.ServiceCheck, hasProvider bool) {
	switch {
	case sc.Err != nil:
		_, _ = fmt.Fprintf(out, "  ✗ %s: %v\n", sc.Service, sc.Err)
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
	checkCmd.Flags().String("config", "config.yml", "Path to orchestrator config file")
	checkCmd.Flags().String("repo-dir", "", "Existing IaC repo checkout to validate (default: clone into a temp dir)")
}

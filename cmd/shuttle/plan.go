package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/neikow/shuttle/internal/secrets"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Preview what a reconcile would deploy (desired vs actual diff)",
	Long: `Shows the actions a reconcile would take — which services would be created,
updated, left unchanged, or torn down — without dispatching anything.

Two modes:

  • Remote (--url + --token): asks a running orchestrator, which diffs the repo
    HEAD against its live ledger. Always accurate.
  • Local (--config): clones/loads the IaC repo and diffs against the ledger at
    --data-dir. With no ledger (e.g. CI), every service shows as "create". Needs
    no running orchestrator.

Use --exit-code to exit non-zero (2) when the plan is non-empty, for CI gating.`,
	Example: `  # Against a running orchestrator
  shuttle plan --url https://orchestrator:8080 --token $SHUTTLE_TOKEN

  # Locally in CI (fresh = everything "create"), failing if non-empty
  shuttle plan --config config.yml --exit-code`,
	RunE: runPlan,
}

func runPlan(cmd *cobra.Command, _ []string) error {
	url, _ := cmd.Flags().GetString("url")

	var report orchestrator.PlanReport
	var err error
	if url != "" {
		report, err = planRemote(cmd, url)
	} else {
		report, err = planLocal(cmd)
	}
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	changes := renderPlan(out, report)

	if exitCode, _ := cmd.Flags().GetBool("exit-code"); exitCode && changes > 0 {
		os.Exit(2)
	}
	return nil
}

func planRemote(cmd *cobra.Command, baseURL string) (orchestrator.PlanReport, error) {
	bearer, _ := cmd.Flags().GetString("token")
	if bearer == "" {
		return orchestrator.PlanReport{}, fmt.Errorf("--token is required with --url")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	endpoint := baseURL + "/plan"
	if ref, _ := cmd.Flags().GetString("ref"); ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	body, err := doJSON(cmd.Context(), client, http.MethodGet, endpoint, bearer, nil)
	if err != nil {
		return orchestrator.PlanReport{}, err
	}
	var report orchestrator.PlanReport
	if err := json.Unmarshal(body, &report); err != nil {
		return orchestrator.PlanReport{}, fmt.Errorf("decode plan: %w", err)
	}
	return report, nil
}

func planLocal(cmd *cobra.Command) (orchestrator.PlanReport, error) {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.LoadOrchestratorConfig(configPath)
	if err != nil {
		return orchestrator.PlanReport{}, err
	}
	if cfg.RepoURL == "" {
		return orchestrator.PlanReport{}, fmt.Errorf("repo_url is not set; nothing to plan")
	}

	repoDir, _ := cmd.Flags().GetString("repo-dir")
	cleanup := func() {}
	if repoDir == "" {
		tmp, mkErr := os.MkdirTemp("", "shuttle-plan-")
		if mkErr != nil {
			return orchestrator.PlanReport{}, fmt.Errorf("create temp repo dir: %w", mkErr)
		}
		repoDir = tmp
		cleanup = func() { _ = os.RemoveAll(tmp) }
	}
	defer cleanup()

	// Open the ledger only if it already exists, so a fresh/CI run doesn't
	// create one (and reports everything as "create").
	var store *ledger.Store
	if dataDir, _ := cmd.Flags().GetString("data-dir"); dataDir != "" {
		dbPath := filepath.Join(dataDir, "shuttle.db")
		if _, statErr := os.Stat(dbPath); statErr == nil {
			store, err = ledger.Open(dbPath)
			if err != nil {
				return orchestrator.PlanReport{}, fmt.Errorf("open ledger: %w", err)
			}
			defer func() { _ = store.Close() }()
		}
	}

	secProvider, err := secrets.NewProvider(cfg.SecretsProvider)
	if err != nil {
		return orchestrator.PlanReport{}, fmt.Errorf("secrets provider: %w", err)
	}
	syncer := orchestrator.NewGitSyncer(cfg.RepoURL, cfg.RepoBranch, repoDir, store, nil, secProvider)
	syncer.SetSecretsPaths(cfg.SecretsBasePath, cfg.SecretsPathTemplate)
	syncer.SetGitCredentials(cfg.GitCredentials)
	return syncer.Plan(cmd.Context())
}

// renderPlan prints the plan and returns the number of changed (non-unchanged)
// services.
func renderPlan(out io.Writer, report orchestrator.PlanReport) int {
	_, _ = fmt.Fprintf(out, "Plan against %s:\n\n", shortSHA(report.SHA))
	symbol := map[orchestrator.PlanAction]string{
		orchestrator.PlanCreate:    "+",
		orchestrator.PlanUpdate:    "~",
		orchestrator.PlanRemove:    "-",
		orchestrator.PlanUnchanged: " ",
	}
	var create, update, remove, unchanged int
	for _, e := range report.Services {
		line := fmt.Sprintf("  %s %-9s %s", symbol[e.Action], e.Action, e.Service)
		if e.Host != "" {
			line += fmt.Sprintf(" (host=%s)", e.Host)
		}
		switch e.Action {
		case orchestrator.PlanCreate:
			create++
		case orchestrator.PlanUpdate:
			update++
			line += fmt.Sprintf("  %s → %s", shortSHA(e.CurrentSHA), shortSHA(e.DesiredSHA))
		case orchestrator.PlanRemove:
			remove++
			line += fmt.Sprintf("  %s → (gone)", shortSHA(e.CurrentSHA))
		case orchestrator.PlanUnchanged:
			unchanged++
		}
		_, _ = fmt.Fprintln(out, line)
	}
	if len(report.Services) == 0 {
		_, _ = fmt.Fprintln(out, "  (no services)")
	}
	_, _ = fmt.Fprintf(out, "\n%d to create, %d to update, %d to remove, %d unchanged.\n",
		create, update, remove, unchanged)
	return create + update + remove
}

func init() {
	planCmd.Flags().String("url", "", "Orchestrator control-plane URL for a remote plan, e.g. https://orchestrator:8080")
	planCmd.Flags().String("token", "", "Control-plane bearer token (required with --url)")
	planCmd.Flags().String("ref", "", "Git ref to plan instead of the orchestrator's branch HEAD (remote mode; branch, tag, refs/pull/N/head, or SHA)")
	planCmd.Flags().String("config", "config.yml", "Path to orchestrator config (local mode)")
	planCmd.Flags().String("repo-dir", "", "Existing IaC checkout to plan against (local mode; default: temp clone)")
	planCmd.Flags().String("data-dir", "", "Data dir holding the ledger for an accurate local diff (default: none → all create)")
	planCmd.Flags().Bool("exit-code", false, "Exit 2 if the plan is non-empty (for CI gating)")
}

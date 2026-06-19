package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/neikow/shuttle/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "shuttle",
	Short: "Self-hosted, git-driven deployment platform",
	Long: `Shuttle is a self-hosted IaC deployment platform shipped as a single binary.

The orchestrator watches a git repo of Docker Compose services and dispatches
deploys to agents running on your managed hosts, recording every deploy in an
append-only ledger that powers rollback and drift detection.

Typical setup:

  1. Run the orchestrator where it can reach your hosts:
       shuttle orchestrator --config config.yml
  2. Enroll each host to get its agent start command:
       shuttle enroll --url https://orchestrator:8080 --token <bearer>
  3. Run the printed command on the host to start its agent:
       shuttle agent --orchestrator orchestrator:9090 --host web-1 --token <tok>

Validate an IaC change before it ships with 'shuttle check'.`,
	Example: `  # Validate config + repo + secrets without deploying
  shuttle check --config config.yml

  # Enroll a host and print its agent command
  shuttle enroll --url https://orchestrator:8080 --token $SHUTTLE_TOKEN

  # Delete volumes of services removed from the repo
  shuttle prune --url https://orchestrator:8080 --token $SHUTTLE_TOKEN`,
	PersistentPreRun: func(cmd *cobra.Command, _ []string) {
		if debug, _ := cmd.Flags().GetBool("debug"); debug {
			slog.SetLogLoggerLevel(slog.LevelDebug)
		}
	},
}

func main() {
	// Load CWD/.env into the environment before any command runs, so config
	// (e.g. the INFISICAL_* vars) can be supplied from a local .env file. The
	// real environment always wins; a missing .env is not an error.
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "shuttle:", err)
		os.Exit(1)
	}
	rootCmd.PersistentFlags().Bool("debug", false, "Enable verbose debug logging")
	rootCmd.AddGroup(
		&cobra.Group{ID: "services", Title: "Long-running services:"},
		&cobra.Group{ID: "ops", Title: "Operations (talk to a running orchestrator):"},
		&cobra.Group{ID: "tools", Title: "Local tools:"},
	)
	orchestratorCmd.GroupID = "services"
	agentCmd.GroupID = "services"
	enrollCmd.GroupID = "ops"
	pruneCmd.GroupID = "ops"
	webhookCmd.GroupID = "ops"
	eventsCmd.GroupID = "ops"
	auditCmd.GroupID = "ops"
	checkCmd.GroupID = "tools"
	planCmd.GroupID = "tools"
	versionCmd.GroupID = "tools"
	initCmd.GroupID = "tools"
	backupCmd.GroupID = "tools"
	restoreCmd.GroupID = "tools"
	rootCmd.AddCommand(versionCmd, orchestratorCmd, agentCmd, enrollCmd, pruneCmd, checkCmd, webhookCmd, planCmd, eventsCmd, initCmd, backupCmd, restoreCmd, auditCmd)
	silenceUsageOnRun(rootCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// silenceUsageOnRun makes every command print its usage on misuse (bad args or
// missing required flags) but not on a runtime failure. Cobra validates flags
// before RunE, so we flip SilenceUsage to true only once RunE is reached: a
// validation error never gets there and still shows usage, while an error
// returned from the command body prints just the error.
func silenceUsageOnRun(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			c.SilenceUsage = true
			return run(c, args)
		}
	}
	for _, sub := range cmd.Commands() {
		silenceUsageOnRun(sub)
	}
}

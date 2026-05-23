package main

import (
	"fmt"
	"os"

	"github.com/neikow/shuttle/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "shuttle",
	Short: "Self-hosted git-driven deployment platform",
	// A RunE failure is a runtime/validation error (e.g. `check` finding a
	// missing secret), not a misuse of the CLI, so don't dump the usage text
	// after it — just the error (cobra still prints that). Inherited by every
	// subcommand. `--help` still shows usage on demand.
	SilenceUsage: true,
}

func main() {
	// Load CWD/.env into the environment before any command runs, so config
	// (e.g. the INFISICAL_* vars) can be supplied from a local .env file. The
	// real environment always wins; a missing .env is not an error.
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "shuttle:", err)
		os.Exit(1)
	}
	rootCmd.AddCommand(versionCmd, orchestratorCmd, agentCmd, enrollCmd, pruneCmd, checkCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

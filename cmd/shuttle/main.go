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
}

func main() {
	// Load CWD/.env into the environment before any command runs, so config
	// (e.g. the INFISICAL_* vars) can be supplied from a local .env file. The
	// real environment always wins; a missing .env is not an error.
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "shuttle:", err)
		os.Exit(1)
	}
	rootCmd.AddCommand(versionCmd, orchestratorCmd, agentCmd, enrollCmd, pruneCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

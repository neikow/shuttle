package main

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "shuttle",
	Short: "Self-hosted git-driven deployment platform",
}

func main() {
	rootCmd.AddCommand(versionCmd, orchestratorCmd, agentCmd, enrollCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

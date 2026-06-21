package main

import (
	"os"

	"github.com/neikow/shuttle/internal/lsp"
	"github.com/spf13/cobra"
)

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Run the Shuttle language server (LSP over stdio)",
	Long: `Runs a Language Server Protocol server over stdio that provides schema-aware
completion and live validation for Shuttle's IaC YAML files — hosts.yaml,
services/<name>/<name>.yaml, dns.yml, orchestrator.yaml, and the orchestrator
config.yml. It reuses the same config loader the orchestrator uses, so the editor
experience stays in lockstep with what Shuttle actually accepts.

This command speaks LSP on stdin/stdout and is meant to be launched by an editor
(see the VS Code extension under editors/vscode), not run interactively.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return lsp.NewServer(os.Stdin, os.Stdout, Version).Run()
	},
}

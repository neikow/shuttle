package main

import (
	"fmt"
	"path/filepath"

	"github.com/neikow/shuttle/internal/ledger"
	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Write a consistent snapshot of the deploy ledger to a file",
	Long: `Snapshots the orchestrator's SQLite deploy ledger to a single file using
SQLite's VACUUM INTO, producing a plain (non-WAL) database safe to copy or
archive. Safe to run while the orchestrator is live — the snapshot is a
consistent point-in-time copy.

The output is a complete ledger: restore it with 'shuttle restore'.`,
	Example: `  shuttle backup --data-dir /var/lib/shuttle --out /backups/shuttle-$(date +%F).db`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, _ := cmd.Flags().GetString("data-dir")
		out, _ := cmd.Flags().GetString("out")
		if out == "" {
			return fmt.Errorf("--out is required")
		}
		dbPath := filepath.Join(dataDir, ledger.DBFileName)
		store, err := ledger.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open ledger %s: %w", dbPath, err)
		}
		defer func() { _ = store.Close() }()

		if err := store.BackupTo(cmd.Context(), out); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Ledger backed up to %s\n", out)
		return nil
	},
}

var restoreCmd = &cobra.Command{
	Use:   "restore <backup-file>",
	Short: "Restore the deploy ledger from a backup file",
	Long: `Validates a backup produced by 'shuttle backup' and installs it as the
orchestrator's ledger in --data-dir, replacing any existing ledger.

Stop the orchestrator first: restoring under a running orchestrator corrupts
in-flight state. The restore is atomic (temp file + rename) and clears stale
WAL/SHM sidecars so the restored snapshot is authoritative.`,
	Example: `  shuttle restore --data-dir /var/lib/shuttle /backups/shuttle-2026-06-18.db`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir, _ := cmd.Flags().GetString("data-dir")
		src := args[0]
		if err := ledger.RestoreInto(src, dataDir); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Ledger restored from %s into %s. Start the orchestrator to resume.\n", src, dataDir)
		return nil
	},
}

func init() {
	backupCmd.Flags().String("data-dir", "./data", "Data directory holding the ledger (shuttle.db)")
	backupCmd.Flags().String("out", "", "Destination file for the snapshot (required; must not exist)")
	_ = backupCmd.MarkFlagRequired("out")

	restoreCmd.Flags().String("data-dir", "./data", "Data directory to restore the ledger into")
}

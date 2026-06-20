package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// backupRecord mirrors the orchestrator's ledger.BackupRecord JSON.
type backupRecord struct {
	BackupID    string     `json:"backup_id"`
	Service     string     `json:"service"`
	Host        string     `json:"host"`
	Engine      string     `json:"engine"`
	Store       string     `json:"store"`
	SnapshotID  string     `json:"snapshot_id"`
	SizeBytes   int64      `json:"size_bytes"`
	Status      string     `json:"status"`
	TriggeredBy string     `json:"triggered_by"`
	Error       string     `json:"error"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

var backupServiceCmd = &cobra.Command{
	Use:   "backup-service <service>",
	Short: "Trigger a backup of a service's data on a running orchestrator",
	Long: `Asks the orchestrator to back up a service's persistent data now (the named
volumes or a postgres dump, per the service's backup policy in the IaC repo).
The orchestrator dispatches the job to the agent on the service's host and
records it in the ledger; inspect progress with 'shuttle backups'.`,
	Example: `  shuttle backup-service db --url https://orchestrator:8080 --token $SHUTTLE_TOKEN`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		service := args[0]
		endpoint := strings.TrimRight(baseURL, "/") + "/backup/" + url.PathEscape(service)
		client := &http.Client{Timeout: 30 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodPost, endpoint, bearer, nil)
		if err != nil {
			return err
		}
		var resp struct {
			BackupID string `json:"backup_id"`
			Host     string `json:"host"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Backup queued: %s (service %s on %s)\n", resp.BackupID, service, resp.Host)
		return nil
	},
}

var backupsCmd = &cobra.Command{
	Use:   "backups",
	Short: "List service-data backups recorded by a running orchestrator",
	Long: `Lists backup attempts the orchestrator has recorded: when, which service,
engine/store, status, size, and snapshot id. Filter to one service with
--service and cap the rows with --limit.`,
	Example: `  shuttle backups --url https://orchestrator:8080 --token $SHUTTLE_TOKEN --service db`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		service, _ := cmd.Flags().GetString("service")
		limit, _ := cmd.Flags().GetInt("limit")

		q := url.Values{}
		if service != "" {
			q.Set("service", service)
		}
		if limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/backups"
		if enc := q.Encode(); enc != "" {
			endpoint += "?" + enc
		}
		client := &http.Client{Timeout: 30 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodGet, endpoint, bearer, nil)
		if err != nil {
			return err
		}
		var records []backupRecord
		if err := json.Unmarshal(body, &records); err != nil {
			return fmt.Errorf("decode backups response: %w", err)
		}
		out := cmd.OutOrStdout()
		if len(records) == 0 {
			_, _ = fmt.Fprintln(out, "No backups.")
			return nil
		}
		for _, b := range records {
			line := fmt.Sprintf("%s  %-8s  %-10s  %-8s/%-6s  %-12s  size=%s",
				b.StartedAt.Format(time.RFC3339), b.Status, b.Service, b.Engine, b.Store,
				b.BackupID, humanBytes(b.SizeBytes))
			if b.SnapshotID != "" {
				line += "  snap=" + b.SnapshotID
			}
			if b.Error != "" {
				line += "  err=" + b.Error
			}
			_, _ = fmt.Fprintln(out, line)
		}
		return nil
	},
}

var restoreServiceCmd = &cobra.Command{
	Use:   "restore-service <service>",
	Short: "Restore a service's data from a backup (DESTRUCTIVE)",
	Long: `Restores a service's data from a prior backup. The orchestrator stops the
service's containers, restores the snapshot into its volumes (or replays the
postgres dump), and starts the service again — overwriting current data.

Without --backup-id the most recent successful backup is used. This is an
admin-tier, destructive action; pass --yes to skip the confirmation prompt.`,
	Example: `  shuttle restore-service db --url https://orchestrator:8080 --token $SHUTTLE_TOKEN
  shuttle restore-service db --backup-id 1718900000-7 --yes --url … --token …`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		service := args[0]
		backupID, _ := cmd.Flags().GetString("backup-id")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			target := "the most recent successful backup"
			if backupID != "" {
				target = "backup " + backupID
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"This OVERWRITES %s's current data with %s.\nRe-run with --yes to proceed.\n", service, target)
			return nil
		}

		q := url.Values{}
		q.Set("service", service)
		if backupID != "" {
			q.Set("backup_id", backupID)
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/restore?" + q.Encode()
		client := &http.Client{Timeout: 60 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodPost, endpoint, bearer, nil)
		if err != nil {
			return err
		}
		var resp struct {
			OperationID string `json:"operation_id"`
			Host        string `json:"host"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Restore queued: %s (service %s on %s, snapshot %s)\n", resp.OperationID, service, resp.Host, resp.SnapshotID)
		return nil
	},
}

// humanBytes renders a byte count compactly (e.g. 4.0K, 12M).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

func init() {
	for _, c := range []*cobra.Command{backupServiceCmd, backupsCmd, restoreServiceCmd} {
		c.Flags().String("url", "", "Orchestrator control-plane URL (required)")
		c.Flags().String("token", "", "Control-plane bearer token (required)")
		_ = c.MarkFlagRequired("url")
		_ = c.MarkFlagRequired("token")
	}
	backupsCmd.Flags().String("service", "", "Filter to one service")
	backupsCmd.Flags().Int("limit", 50, "Maximum number of rows (1-200)")
	restoreServiceCmd.Flags().String("backup-id", "", "Backup to restore (default: most recent successful)")
	restoreServiceCmd.Flags().Bool("yes", false, "Skip the destructive-action confirmation")
}

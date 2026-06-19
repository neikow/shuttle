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

type auditEntry struct {
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	SourceIP string    `json:"source_ip"`
	Result   string    `json:"result"`
	Detail   string    `json:"detail"`
}

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Show the orchestrator's control-plane audit log",
	Long: `Talks to a running orchestrator and prints the audit log: who did what
(deploy, rollback, prune, enrollment, webhook CRUD), when, from where, and how
it turned out. Use --action to filter to one action and --limit to cap the row
count.`,
	Example: `  shuttle audit --url https://orchestrator:8080 --token $SHUTTLE_TOKEN
  shuttle audit --url https://orchestrator:8080 --token $SHUTTLE_TOKEN --action deploy --limit 100`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		action, _ := cmd.Flags().GetString("action")
		limit, _ := cmd.Flags().GetInt("limit")

		baseURL = strings.TrimRight(baseURL, "/")
		q := url.Values{}
		if action != "" {
			q.Set("action", action)
		}
		if limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		endpoint := baseURL + "/audit"
		if enc := q.Encode(); enc != "" {
			endpoint += "?" + enc
		}

		client := &http.Client{Timeout: 30 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodGet, endpoint, bearer, nil)
		if err != nil {
			return err
		}
		var entries []auditEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return fmt.Errorf("decode audit response: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(entries) == 0 {
			_, _ = fmt.Fprintln(out, "No audit entries.")
			return nil
		}
		for _, e := range entries {
			line := fmt.Sprintf("%s  %-8s  %-14s  %-20s  actor=%s",
				e.At.Format(time.RFC3339), e.Result, e.Action, e.Target, e.Actor)
			if e.SourceIP != "" {
				line += "  ip=" + e.SourceIP
			}
			if e.Detail != "" {
				line += "  " + e.Detail
			}
			_, _ = fmt.Fprintln(out, line)
		}
		return nil
	},
}

func init() {
	auditCmd.Flags().String("url", "", "Orchestrator control-plane URL, e.g. https://orchestrator:8080 (required)")
	auditCmd.Flags().String("token", "", "Control-plane bearer token (required)")
	auditCmd.Flags().String("action", "", "Filter to one action (deploy, rollback, prune, enroll, enroll.redeem, webhook.create, webhook.delete)")
	auditCmd.Flags().Int("limit", 50, "Maximum number of entries to show (1-200)")
	_ = auditCmd.MarkFlagRequired("url")
	_ = auditCmd.MarkFlagRequired("token")
}

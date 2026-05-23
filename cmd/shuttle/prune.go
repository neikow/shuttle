package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type pruneResult struct {
	Pruned []string `json:"pruned"`
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete the volumes of services removed from the repo",
	Long: `Talks to a running orchestrator's control plane and force-deletes the named
volumes of every service that has been removed from the IaC repo but whose
volumes were kept (the default "manual" delete_volumes policy, or a duration
that has not yet elapsed). This is irreversible.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		baseURL = strings.TrimRight(baseURL, "/")
		client := &http.Client{Timeout: 30 * time.Second}

		body, err := doJSON(cmd.Context(), client, http.MethodPost, baseURL+"/prune", bearer, nil)
		if err != nil {
			return err
		}
		var res pruneResult
		if err := json.Unmarshal(body, &res); err != nil {
			return fmt.Errorf("decode prune response: %w", err)
		}

		out := cmd.OutOrStdout()
		if len(res.Pruned) == 0 {
			_, _ = fmt.Fprintln(out, "No volumes to prune.")
			return nil
		}
		_, _ = fmt.Fprintf(out, "Pruned volumes for %d service(s):\n", len(res.Pruned))
		for _, svc := range res.Pruned {
			_, _ = fmt.Fprintf(out, "  - %s\n", svc)
		}
		return nil
	},
}

func init() {
	pruneCmd.Flags().String("url", "", "Orchestrator control-plane URL (e.g. https://orchestrator:8080)")
	pruneCmd.Flags().String("token", "", "Control-plane bearer token")
}

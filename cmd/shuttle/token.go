package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type controlToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage named, role-scoped control-plane tokens (read/deploy/admin)",
	Long: `Create, list, and revoke named control-plane bearer tokens, each scoped to a
role: read (inspect), deploy (deploy/rollback/prune), or admin (everything,
including token and agent management). The orchestrator's static bearer_token
remains the bootstrap admin; these tokens add least-privilege credentials whose
names appear in the audit log as the actor. All subcommands talk to a running
orchestrator and require an admin token.`,
}

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Mint a new role-scoped token (printed once)",
	Example: `  shuttle token create --url https://orchestrator:8080 --token $ADMIN_TOKEN \
    --name ci-bot --role deploy`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, bearer, err := tokenCreds(cmd)
		if err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		role, _ := cmd.Flags().GetString("role")
		if name == "" || role == "" {
			return fmt.Errorf("--name and --role are required")
		}
		reqBody, _ := json.Marshal(map[string]string{"name": name, "role": role})

		client := &http.Client{Timeout: 30 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodPost, baseURL+"/tokens", bearer, reqBody)
		if err != nil {
			return err
		}
		var res struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Role  string `json:"role"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return fmt.Errorf("decode create response: %w", err)
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Created token %q (role=%s, id=%s).\n", res.Name, res.Role, res.ID)
		_, _ = fmt.Fprintf(out, "Token (shown once, store it now):\n\n  %s\n", res.Token)
		return nil
	},
}

var tokenListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List control-plane tokens (names + roles, never the secret)",
	Example: `  shuttle token list --url https://orchestrator:8080 --token $ADMIN_TOKEN`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, bearer, err := tokenCreds(cmd)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 30 * time.Second}
		body, _, err := doJSON(cmd.Context(), client, http.MethodGet, baseURL+"/tokens", bearer, nil)
		if err != nil {
			return err
		}
		var tokens []controlToken
		if err := json.Unmarshal(body, &tokens); err != nil {
			return fmt.Errorf("decode list response: %w", err)
		}
		out := cmd.OutOrStdout()
		if len(tokens) == 0 {
			_, _ = fmt.Fprintln(out, "No control tokens.")
			return nil
		}
		for _, t := range tokens {
			status := "active"
			if t.RevokedAt != nil {
				status = "revoked " + t.RevokedAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(out, "%-20s  %-7s  %-8s  created=%s  id=%s\n",
				t.Name, t.Role, status, t.CreatedAt.Format(time.RFC3339), t.ID)
		}
		return nil
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:     "revoke <id>",
	Short:   "Revoke a control-plane token by ID",
	Args:    cobra.ExactArgs(1),
	Example: `  shuttle token revoke 1718800000000000000-7 --url https://orchestrator:8080 --token $ADMIN_TOKEN`,
	RunE: func(cmd *cobra.Command, args []string) error {
		baseURL, bearer, err := tokenCreds(cmd)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 30 * time.Second}
		if _, _, err := doJSON(cmd.Context(), client, http.MethodDelete, baseURL+"/tokens/"+args[0], bearer, nil); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked token %s.\n", args[0])
		return nil
	},
}

// tokenCreds reads the shared --url/--token flags and validates them.
func tokenCreds(cmd *cobra.Command) (baseURL, bearer string, err error) {
	baseURL, _ = cmd.Flags().GetString("url")
	bearer, _ = cmd.Flags().GetString("token")
	if baseURL == "" || bearer == "" {
		return "", "", fmt.Errorf("--url and --token are required")
	}
	return strings.TrimRight(baseURL, "/"), bearer, nil
}

func init() {
	tokenCmd.PersistentFlags().String("url", "", "Orchestrator control-plane URL, e.g. https://orchestrator:8080 (required)")
	tokenCmd.PersistentFlags().String("token", "", "Admin bearer token (required)")
	tokenCreateCmd.Flags().String("name", "", "Human-readable token name (becomes the audit actor) (required)")
	tokenCreateCmd.Flags().String("role", "", "Role: read, deploy, or admin (required)")
	tokenCmd.AddCommand(tokenCreateCmd, tokenListCmd, tokenRevokeCmd)
}

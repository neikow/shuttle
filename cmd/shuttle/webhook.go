package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage per-service git repo webhooks",
	Long: `Create, list, and delete per-service repo webhooks on a running orchestrator.

A repo webhook gives your git provider a URL to call on push; the orchestrator
then reconciles that service. Run 'create' to mint a webhook URL, paste it into
your provider (GitHub → Settings → Webhooks), and the service auto-deploys on
push.`,
}

var webhookCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a repo webhook for a service and print its URL",
	Example: `  shuttle webhook create --url https://orchestrator:8080 \
    --token $SHUTTLE_TOKEN --service web`,
	RunE: runWebhookCreate,
}

var webhookListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List configured repo webhooks",
	Example: `  shuttle webhook list --url https://orchestrator:8080 --token $SHUTTLE_TOKEN`,
	RunE:    runWebhookList,
}

var webhookDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a repo webhook by ID",
	Example: `  shuttle webhook delete 3f9a... --url https://orchestrator:8080 --token $SHUTTLE_TOKEN`,
	Args:    cobra.ExactArgs(1),
	RunE:    runWebhookDelete,
}

func init() {
	webhookCreateCmd.Flags().String("url", "", "Orchestrator HTTP URL, e.g. https://orchestrator:8080 (required)")
	webhookCreateCmd.Flags().String("token", "", "Control-plane bearer token (required)")
	webhookCreateCmd.Flags().String("service", "", "Service to attach the webhook to (required)")
	webhookCreateCmd.Flags().String("base-url", "", "Public base URL for the printed webhook URL (defaults to --url)")
	_ = webhookCreateCmd.MarkFlagRequired("url")
	_ = webhookCreateCmd.MarkFlagRequired("token")
	_ = webhookCreateCmd.MarkFlagRequired("service")

	webhookListCmd.Flags().String("url", "", "Orchestrator HTTP URL, e.g. https://orchestrator:8080 (required)")
	webhookListCmd.Flags().String("token", "", "Control-plane bearer token (required)")
	_ = webhookListCmd.MarkFlagRequired("url")
	_ = webhookListCmd.MarkFlagRequired("token")

	webhookDeleteCmd.Flags().String("url", "", "Orchestrator HTTP URL, e.g. https://orchestrator:8080 (required)")
	webhookDeleteCmd.Flags().String("token", "", "Control-plane bearer token (required)")
	_ = webhookDeleteCmd.MarkFlagRequired("url")
	_ = webhookDeleteCmd.MarkFlagRequired("token")

	webhookCmd.AddCommand(webhookCreateCmd, webhookListCmd, webhookDeleteCmd)
}

func runWebhookCreate(cmd *cobra.Command, _ []string) error {
	baseURL, _ := cmd.Flags().GetString("url")
	token, _ := cmd.Flags().GetString("token")
	service, _ := cmd.Flags().GetString("service")
	publicBase, _ := cmd.Flags().GetString("base-url")
	if publicBase == "" {
		publicBase = baseURL
	}

	body := strings.NewReader(`{"service":"` + service + `"}`)
	req, _ := http.NewRequestWithContext(cmd.Context(), http.MethodPost, baseURL+"/webhooks/repo", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	webhookURL := strings.TrimRight(publicBase, "/") + "/webhook/repo/" + result.ID
	fmt.Printf("Webhook ID:  %s\n", result.ID)
	fmt.Printf("Webhook URL: %s\n", webhookURL)
	fmt.Printf("\nConfigure this URL in your git provider (GitHub → Settings → Webhooks).\n")
	fmt.Printf("Content type: application/json\n")
	return nil
}

func runWebhookList(cmd *cobra.Command, _ []string) error {
	baseURL, _ := cmd.Flags().GetString("url")
	token, _ := cmd.Flags().GetString("token")

	req, _ := http.NewRequestWithContext(cmd.Context(), http.MethodGet, baseURL+"/webhooks/repo", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var webhooks []struct {
		ID        string    `json:"ID"`
		Service   string    `json:"Service"`
		CreatedAt time.Time `json:"CreatedAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&webhooks); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(webhooks) == 0 {
		fmt.Println("No repo webhooks configured.")
		return nil
	}
	fmt.Printf("%-64s  %-20s  %s\n", "ID", "SERVICE", "CREATED")
	fmt.Printf("%-64s  %-20s  %s\n", strings.Repeat("-", 64), strings.Repeat("-", 20), strings.Repeat("-", 20))
	for _, w := range webhooks {
		fmt.Printf("%-64s  %-20s  %s\n", w.ID, w.Service, w.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func runWebhookDelete(cmd *cobra.Command, args []string) error {
	baseURL, _ := cmd.Flags().GetString("url")
	token, _ := cmd.Flags().GetString("token")
	id := args[0]

	req, _ := http.NewRequestWithContext(cmd.Context(), http.MethodDelete, baseURL+"/webhooks/repo/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("webhook %s not found", id)
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	fmt.Printf("Webhook %s deleted.\n", id)
	return nil
}

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/spf13/cobra"
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Stream live orchestrator events (deploys, drift, teardown)",
	Long: `Connects to a running orchestrator's Server-Sent Events stream and prints
events as they happen: deploys queued/succeeded/failed, rollbacks, drift
detection, service teardown, and volume purges.

On connect the orchestrator replays its recent event backlog, then streams live
events until you disconnect (Ctrl+C).`,
	Example: `  shuttle events --url https://orchestrator:8080 --token $SHUTTLE_TOKEN`,
	RunE:    runEvents,
}

func runEvents(cmd *cobra.Command, _ []string) error {
	baseURL, _ := cmd.Flags().GetString("url")
	bearer, _ := cmd.Flags().GetString("token")
	baseURL = strings.TrimRight(baseURL, "/")

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, baseURL+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Connected. Streaming events (Ctrl+C to stop)...")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue // blank lines and `: keep-alive` comments
		}
		var ev orchestrator.Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		printEvent(out, ev)
	}
	return scanner.Err()
}

func printEvent(out io.Writer, ev orchestrator.Event) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %-22s", ev.Time.Format(time.RFC3339), ev.Type)
	for _, f := range []struct{ k, v string }{
		{"service", ev.Service}, {"host", ev.Host},
		{"deploy_id", ev.DeployID}, {"sha", shortSHA(ev.SHA)},
		{"status", ev.Status},
	} {
		if f.v != "" {
			fmt.Fprintf(&b, " %s=%s", f.k, f.v)
		}
	}
	if ev.Message != "" {
		fmt.Fprintf(&b, " (%s)", ev.Message)
	}
	_, _ = fmt.Fprintln(out, b.String())
}

func init() {
	eventsCmd.Flags().String("url", "", "Orchestrator control-plane URL, e.g. https://orchestrator:8080 (required)")
	eventsCmd.Flags().String("token", "", "Control-plane bearer token (required)")
	_ = eventsCmd.MarkFlagRequired("url")
	_ = eventsCmd.MarkFlagRequired("token")
}

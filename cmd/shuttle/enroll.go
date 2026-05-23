package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type enrollHost struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type enrollResult struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Token   string `json:"token"`
	Command string `json:"command"`
	TLS     bool   `json:"tls"`
}

var enrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll a new agent: pick a host and print its start command",
	Long: `Talks to a running orchestrator's control plane. Lists the hosts declared
in the IaC repo, lets you pick one (or pass --host), mints a host-scoped agent
token, and prints the ready-to-run agent command.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		host, _ := cmd.Flags().GetString("host")
		if baseURL == "" || bearer == "" {
			return fmt.Errorf("--url and --token are required")
		}
		baseURL = strings.TrimRight(baseURL, "/")
		client := &http.Client{Timeout: 30 * time.Second}

		ctx := cmd.Context()
		if host == "" {
			hosts, err := listHosts(ctx, client, baseURL, bearer)
			if err != nil {
				return err
			}
			if len(hosts) == 0 {
				return fmt.Errorf("no hosts declared in the IaC repo")
			}
			host, err = pickHost(cmd.OutOrStdout(), hosts)
			if err != nil {
				return err
			}
		}

		res, err := enrollHostReq(ctx, client, baseURL, bearer, host)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "\nEnrolled host %q (token id %s).\n\nRun the agent with:\n\n  %s\n\n",
			res.Host, res.ID, res.Command)
		if res.TLS {
			fmt.Fprintf(out, "TLS is enabled; if the orchestrator uses a private CA, add: --ca <path-to-ca.crt>\n")
		}
		fmt.Fprintf(out, "Keep the token secret — it is shown only once. Revoke it from the ledger if leaked.\n")
		return nil
	},
}

func listHosts(ctx context.Context, client *http.Client, baseURL, bearer string) ([]enrollHost, error) {
	body, err := doJSON(ctx, client, http.MethodGet, baseURL+"/hosts", bearer, nil)
	if err != nil {
		return nil, err
	}
	var hosts []enrollHost
	if err := json.Unmarshal(body, &hosts); err != nil {
		return nil, fmt.Errorf("decode hosts: %w", err)
	}
	return hosts, nil
}

func pickHost(out io.Writer, hosts []enrollHost) (string, error) {
	fmt.Fprintln(out, "Available hosts:")
	for i, h := range hosts {
		label := ""
		if len(h.Labels) > 0 {
			pairs := make([]string, 0, len(h.Labels))
			for k, v := range h.Labels {
				pairs = append(pairs, k+"="+v)
			}
			label = "  (" + strings.Join(pairs, ", ") + ")"
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, h.Name, label)
	}
	fmt.Fprint(out, "Select a host [1]: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return hosts[0].Name, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(hosts) {
		return "", fmt.Errorf("invalid selection %q", line)
	}
	return hosts[n-1].Name, nil
}

func enrollHostReq(ctx context.Context, client *http.Client, baseURL, bearer, host string) (*enrollResult, error) {
	reqBody, _ := json.Marshal(map[string]string{"host": host})
	body, err := doJSON(ctx, client, http.MethodPost, baseURL+"/enroll", bearer, reqBody)
	if err != nil {
		return nil, err
	}
	var res enrollResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("decode enroll response: %w", err)
	}
	return &res, nil
}

func doJSON(ctx context.Context, client *http.Client, method, url, bearer string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func init() {
	enrollCmd.Flags().String("url", "", "Orchestrator control-plane URL (e.g. https://orchestrator:8080)")
	enrollCmd.Flags().String("token", "", "Control-plane bearer token")
	enrollCmd.Flags().String("host", "", "Host to enroll (skips the interactive picker)")
	_ = enrollCmd.MarkFlagRequired("url")
	_ = enrollCmd.MarkFlagRequired("token")
}

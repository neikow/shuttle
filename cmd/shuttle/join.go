package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/neikow/shuttle/internal/agent"
	"github.com/neikow/shuttle/internal/mtls"
	"github.com/spf13/cobra"
)

const (
	agentTokenFile = "agent.token"
	orchestratorCA = "orchestrator-ca.pem"
)

// redeemResult mirrors the orchestrator's /enroll/redeem response.
type redeemResult struct {
	Token      string `json:"token"`
	Host       string `json:"host"`
	GRPCAddr   string `json:"grpc_addr"`
	ServerName string `json:"server_name"`
	TLS        bool   `json:"tls"`
	CAPEM      string `json:"ca_pem"`
}

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Redeem a join token on this host, then run the agent",
	Long: `Run once on a managed host with the command printed by 'shuttle enroll'.

join exchanges the single-use join token for a long-lived host-scoped agent
credential, persists it (and the orchestrator CA) under --work-dir at mode
0600, and then starts the agent. The orchestrator's TLS certificate is pinned
via --pin (trust-on-first-use), so no CA file has to be copied to the host.

After the first join, restart the agent with a plain 'shuttle agent
--orchestrator <addr> --host <host>': it auto-loads the persisted token and CA
from --work-dir.`,
	Example: `  # Command as printed by 'shuttle enroll'
  shuttle agent join --redeem-url https://orchestrator:8080 --token <join-token> --pin sha256:...`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		redeemURL, _ := cmd.Flags().GetString("redeem-url")
		joinToken, _ := cmd.Flags().GetString("token")
		pin, _ := cmd.Flags().GetString("pin")
		workDir, _ := cmd.Flags().GetString("work-dir")
		driverName, _ := cmd.Flags().GetString("driver")
		dockerBin, _ := cmd.Flags().GetString("docker-bin")
		if redeemURL == "" || joinToken == "" {
			return fmt.Errorf("--redeem-url and --token are required")
		}

		driver, err := agent.NewDriver(driverName, dockerBin)
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		res, err := redeemJoinToken(ctx, strings.TrimRight(redeemURL, "/"), joinToken, pin)
		if err != nil {
			return err
		}

		caFile, err := persistCredentials(workDir, res)
		if err != nil {
			return err
		}
		slog.Info("join redeemed; agent credentials persisted", "host", res.Host, "work_dir", workDir)

		cfg := agent.Config{
			Host:         res.Host,
			Orchestrator: res.GRPCAddr,
			AgentVersion: Version,
			WorkDir:      workDir,
			CAFile:       caFile,
			ServerName:   res.ServerName,
			Token:        res.Token,
			DockerBin:    dockerBin,
		}
		return agent.Run(ctx, cfg, driver)
	},
}

func redeemJoinToken(ctx context.Context, redeemURL, joinToken, pin string) (*redeemResult, error) {
	client, err := mtls.PinnedHTTPClient(pin, 30*time.Second)
	if err != nil {
		return nil, err
	}
	reqBody, _ := json.Marshal(map[string]string{"join_token": joinToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, redeemURL+"/enroll/redeem", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("redeem %s: %w", redeemURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("redeem failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var res redeemResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode redeem response: %w", err)
	}
	if res.Token == "" || res.GRPCAddr == "" {
		return nil, fmt.Errorf("redeem response missing token or grpc address")
	}
	return &res, nil
}

// persistCredentials writes the agent token and (optional) orchestrator CA under
// workDir at mode 0600, returning the CA file path (empty if none). A later
// plain `shuttle agent` reuses these.
func persistCredentials(workDir string, res *redeemResult) (string, error) {
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, agentTokenFile), []byte(res.Token), 0o600); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	if res.CAPEM == "" {
		return "", nil
	}
	caPath := filepath.Join(workDir, orchestratorCA)
	if err := os.WriteFile(caPath, []byte(res.CAPEM), 0o600); err != nil {
		return "", fmt.Errorf("write ca: %w", err)
	}
	return caPath, nil
}

func init() {
	joinCmd.Flags().String("redeem-url", "", "Orchestrator control-plane URL to redeem against (required)")
	joinCmd.Flags().String("token", "", "Single-use join token from `shuttle enroll` (required)")
	joinCmd.Flags().String("pin", "", "Orchestrator certificate pin (sha256:...) for trust-on-first-use")
	joinCmd.Flags().String("work-dir", "./agent-work", "Base directory for compose workspaces and persisted credentials")
	joinCmd.Flags().String("driver", "compose", "Deploy driver: 'compose' (Docker Compose) or 'synology' (DSM Container Manager)")
	joinCmd.Flags().String("docker-bin", "", "Override the Docker executable path (e.g. /usr/local/bin/docker on Synology)")
}

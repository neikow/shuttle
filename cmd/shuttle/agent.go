package main

import (
	"os/signal"
	"syscall"

	"github.com/neikow/shuttle/internal/agent"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run the agent on a managed host: receive and run deploys",
	Long: `Runs on each managed host. The agent dials out to the orchestrator (so the
host needs no inbound firewall holes), then receives finished Compose files and
runs them with Docker Compose, reporting container state back for drift healing.

Authenticate one of two ways:

  • Enrollment token (simplest): pass --token from 'shuttle enroll'. Add --ca if
    the orchestrator uses a private CA.
  • Mutual TLS: pass --cert, --key, and --ca.

The easiest path is to run 'shuttle enroll' on the orchestrator, which prints a
ready-to-run agent command for the chosen host.`,
	Example: `  # Start with an enrollment token (command printed by 'shuttle enroll')
  shuttle agent --orchestrator orchestrator:9090 --host web-1 --token <token>

  # Start with mutual TLS instead
  shuttle agent --orchestrator orchestrator:9090 --host web-1 \
    --cert agent.crt --key agent.key --ca ca.crt

  # Synology DSM (Container Manager) host
  shuttle agent --orchestrator orchestrator:9090 --host nas --token <token> --driver synology`,
	RunE: func(cmd *cobra.Command, args []string) error {
		orchestratorAddr, _ := cmd.Flags().GetString("orchestrator")
		host, _ := cmd.Flags().GetString("host")
		workDir, _ := cmd.Flags().GetString("work-dir")
		cert, _ := cmd.Flags().GetString("cert")
		key, _ := cmd.Flags().GetString("key")
		ca, _ := cmd.Flags().GetString("ca")
		serverName, _ := cmd.Flags().GetString("server-name")
		tok, _ := cmd.Flags().GetString("token")
		driverName, _ := cmd.Flags().GetString("driver")
		dockerBin, _ := cmd.Flags().GetString("docker-bin")

		driver, err := agent.NewDriver(driverName, dockerBin)
		if err != nil {
			return err
		}

		cfg := agent.Config{
			Host:         host,
			Orchestrator: orchestratorAddr,
			AgentVersion: Version,
			WorkDir:      workDir,
			CertFile:     cert,
			KeyFile:      key,
			CAFile:       ca,
			ServerName:   serverName,
			Token:        tok,
			DockerBin:    dockerBin,
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		return agent.Run(ctx, cfg, driver)
	},
}

func init() {
	agentCmd.Flags().String("orchestrator", "", "Orchestrator gRPC address to dial, host:port (required)")
	agentCmd.Flags().String("host", "", "This host's name; must match a host in the IaC repo (required)")
	agentCmd.Flags().String("work-dir", "./agent-work", "Base directory for per-service Compose workspaces")
	agentCmd.Flags().String("cert", "", "Agent TLS certificate path (mTLS auth)")
	agentCmd.Flags().String("key", "", "Agent TLS key path (mTLS auth)")
	agentCmd.Flags().String("ca", "", "CA certificate path to verify the orchestrator (private CAs)")
	agentCmd.Flags().String("server-name", "orchestrator", "Expected SAN on the orchestrator's certificate")
	agentCmd.Flags().String("token", "", "Host-scoped enrollment token (from `shuttle enroll`)")
	agentCmd.Flags().String("driver", "compose", "Deploy driver: 'compose' (Docker Compose) or 'synology' (DSM Container Manager)")
	agentCmd.Flags().String("docker-bin", "", "Override the Docker executable path (e.g. /usr/local/bin/docker on Synology)")
	_ = agentCmd.MarkFlagRequired("orchestrator")
	_ = agentCmd.MarkFlagRequired("host")
}

package main

import (
	"os/signal"
	"syscall"

	"github.com/neikow/shuttle/internal/agent"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run the Shuttle agent on a managed host",
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
	agentCmd.Flags().String("orchestrator", "", "Orchestrator gRPC address (host:port)")
	agentCmd.Flags().String("host", "", "Host name (must match hosts.yaml)")
	agentCmd.Flags().String("work-dir", "./agent-work", "Base directory for compose workspaces")
	agentCmd.Flags().String("cert", "", "Path to agent TLS certificate (enables mTLS)")
	agentCmd.Flags().String("key", "", "Path to agent TLS key")
	agentCmd.Flags().String("ca", "", "Path to CA certificate for orchestrator verification")
	agentCmd.Flags().String("server-name", "orchestrator", "Expected SAN on orchestrator certificate")
	agentCmd.Flags().String("token", "", "Agent enrollment token (from `shuttle enroll`)")
	agentCmd.Flags().String("driver", "compose", "Deploy driver: compose | synology")
	agentCmd.Flags().String("docker-bin", "", "Override the Docker executable path (e.g. /usr/local/bin/docker on Synology)")
	_ = agentCmd.MarkFlagRequired("orchestrator")
	_ = agentCmd.MarkFlagRequired("host")
}

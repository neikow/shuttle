package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/mtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Config is the agent runtime configuration.
type Config struct {
	Host         string
	Orchestrator string // host:port
	AgentVersion string
	WorkDir      string // base dir for compose workspaces
	// TLS fields enable mTLS when all three are set; otherwise the agent dials
	// insecurely (dev only). ServerName must match a SAN on the orchestrator cert.
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
}

// Run connects to the orchestrator and processes commands until ctx is cancelled.
func Run(ctx context.Context, cfg Config, driver Driver) error {
	creds := insecure.NewCredentials()
	if cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CAFile != "" {
		var err error
		creds, err = mtls.ClientCreds(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.ServerName)
		if err != nil {
			return fmt.Errorf("build mTLS creds: %w", err)
		}
	}
	conn, err := grpc.NewClient(cfg.Orchestrator,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return fmt.Errorf("dial orchestrator: %w", err)
	}
	defer conn.Close()

	client := shuttlev1.NewAgentServiceClient(conn)

	for {
		if err := runSession(ctx, cfg, client, driver); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("agent session error, reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func runSession(ctx context.Context, cfg Config, client shuttlev1.AgentServiceClient, driver Driver) error {
	stream, err := client.Register(ctx)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	// Send registration.
	if err := stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_Register{
			Register: &shuttlev1.RegisterRequest{
				Host:         cfg.Host,
				AgentVersion: cfg.AgentVersion,
			},
		},
	}); err != nil {
		return err
	}

	// Heartbeat goroutine.
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := stream.Send(&shuttlev1.AgentEvent{
					Payload: &shuttlev1.AgentEvent_Heartbeat{
						Heartbeat: &shuttlev1.Heartbeat{TsUnixMs: time.Now().UnixMilli()},
					},
				}); err != nil {
					return
				}
			}
		}
	}()
	defer func() { <-hbDone }()

	for {
		cmd, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if err := handleCommand(ctx, cfg, stream, driver, cmd); err != nil {
			slog.Error("command error", "err", err)
		}
	}
}

func handleCommand(
	ctx context.Context,
	cfg Config,
	stream shuttlev1.AgentService_RegisterClient,
	driver Driver,
	cmd *shuttlev1.OrchestratorCommand,
) error {
	switch payload := cmd.Payload.(type) {
	case *shuttlev1.OrchestratorCommand_Deploy:
		return executeDeploy(ctx, cfg, stream, driver, payload.Deploy)
	case *shuttlev1.OrchestratorCommand_Rollback:
		return executeRollback(ctx, cfg, stream, driver, payload.Rollback)
	default:
		slog.Warn("unknown command type", "type", fmt.Sprintf("%T", cmd.Payload))
	}
	return nil
}

func executeDeploy(ctx context.Context, cfg Config, stream shuttlev1.AgentService_RegisterClient, driver Driver, req *shuttlev1.DeployRequest) error {
	slog.Info("executing deploy", "deploy_id", req.DeployId, "service", req.Service)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Apply(ctx, ApplyParams{
		Service:     req.Service,
		ComposeYAML: req.ComposeYaml,
		Env:         req.Env,
		WorkDir:     workDir,
	})
	return streamDeployResult(stream, req.DeployId, logCh, err)
}

func executeRollback(ctx context.Context, cfg Config, stream shuttlev1.AgentService_RegisterClient, driver Driver, req *shuttlev1.RollbackRequest) error {
	slog.Info("executing rollback", "deploy_id", req.DeployId, "service", req.Service, "target_sha", req.TargetSha)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Rollback(ctx, RollbackParams{
		Service:     req.Service,
		ComposeYAML: req.ComposeYaml,
		Env:         req.Env,
		WorkDir:     workDir,
	})
	return streamDeployResult(stream, req.DeployId, logCh, err)
}

func streamDeployResult(stream shuttlev1.AgentService_RegisterClient, deployID string, logCh <-chan LogLine, startErr error) error {
	if startErr != nil {
		return stream.Send(&shuttlev1.AgentEvent{
			Payload: &shuttlev1.AgentEvent_DeployResult{
				DeployResult: &shuttlev1.DeployResponse{
					DeployId: deployID,
					Status:   shuttlev1.DeployStatus_DEPLOY_STATUS_FAILED,
					Error:    startErr.Error(),
				},
			},
		})
	}

	var logs []*shuttlev1.LogLine
	success := true
	for line := range logCh {
		logs = append(logs, &shuttlev1.LogLine{
			TsUnixMs: line.TsUnixMs,
			Stream:   line.Stream,
			Text:     line.Text,
		})
		if line.Stream == "stderr" && containsError(line.Text) {
			success = false
		}
	}

	finalStatus := shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS
	if !success {
		finalStatus = shuttlev1.DeployStatus_DEPLOY_STATUS_FAILED
	}

	return stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_DeployResult{
			DeployResult: &shuttlev1.DeployResponse{
				DeployId: deployID,
				Status:   finalStatus,
				Logs:     logs,
			},
		},
	})
}

func containsError(text string) bool {
	lower := text
	// docker compose error indicator in log output.
	return len(lower) > 0 && (containsAny(lower, "[shuttle] compose error"))
}

func containsAny(s string, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

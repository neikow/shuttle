package agent

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/mtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const stateReportInterval = 30 * time.Second

// deployedSet tracks the services this agent has deployed, so it can report
// their container state for orchestrator drift detection. It survives
// reconnects across sessions, and is reseeded from on-disk compose workspaces
// after a process restart (see seedFromDisk).
type deployedSet struct {
	mu sync.RWMutex
	m  map[string]deployedService
}

type deployedService struct {
	workDir string
	sha     string
}

func newDeployedSet() *deployedSet { return &deployedSet{m: make(map[string]deployedService)} }

func (s *deployedSet) put(service, workDir, sha string) {
	s.mu.Lock()
	s.m[service] = deployedService{workDir: workDir, sha: sha}
	s.mu.Unlock()
}

// remove stops tracking a service, so the state-report loop no longer reports
// it. Called after a teardown brings the service down.
func (s *deployedSet) remove(service string) {
	s.mu.Lock()
	delete(s.m, service)
	s.mu.Unlock()
}

// seedFromDisk reconciles the in-memory set with reality after a restart: the
// agent loses its deployed map on process exit, but the compose workspaces it
// wrote persist under baseDir as <baseDir>/<service>/docker-compose.yml. Each
// such workspace is re-tracked so the state-report loop resumes reporting it
// (and the orchestrator can heal a service whose container died while the agent
// was down). The recorded SHA is unknown post-restart and left empty; container
// drift detection keys on status, not SHA. Returns the number of services seeded.
func (s *deployedSet) seedFromDisk(baseDir string) int {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		// No workspaces yet (fresh agent) or unreadable dir: nothing to seed.
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		workDir := filepath.Join(baseDir, e.Name())
		if _, err := os.Stat(filepath.Join(workDir, "docker-compose.yml")); err != nil {
			continue // not a shuttle compose workspace
		}
		s.put(e.Name(), workDir, "")
		n++
	}
	return n
}

func (s *deployedSet) snapshot() map[string]deployedService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]deployedService, len(s.m))
	maps.Copy(out, s.m)
	return out
}

// Config is the agent runtime configuration.
type Config struct {
	Host         string
	Orchestrator string // host:port
	AgentVersion string
	WorkDir      string // base dir for compose workspaces
	// TLS fields. cert+key+ca => mutual TLS. ca only => verify the orchestrator
	// but present no client cert (pairs with token auth). none => insecure (dev).
	// ServerName must match a SAN on the orchestrator cert.
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
	// Token, when set, is sent as a bearer credential to authenticate the agent
	// (see `shuttle enroll`).
	Token string
	// Caddy, when enabled, makes the agent run and manage a Caddy ingress
	// sidecar; the orchestrator pushes this host's routes via CaddyConfigRequest.
	CaddyEnabled bool
	DockerBin    string // docker executable, shared with the Caddy sidecar
}

// tokenCreds attaches a bearer token to every RPC. RequireTransportSecurity is
// false so it also works over the insecure dev channel; production setups pair
// it with server TLS so the token is encrypted in transit.
type tokenCreds struct{ token string }

func (t tokenCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + t.token}, nil
}
func (t tokenCreds) RequireTransportSecurity() bool { return false }

// Run connects to the orchestrator and processes commands until ctx is cancelled.
func Run(ctx context.Context, cfg Config, driver Driver) error {
	creds := insecure.NewCredentials()
	switch {
	case cfg.CertFile != "" && cfg.KeyFile != "":
		var err error
		creds, err = mtls.ClientCreds(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.ServerName)
		if err != nil {
			return fmt.Errorf("build mTLS creds: %w", err)
		}
	case cfg.CAFile != "":
		var err error
		creds, err = mtls.ClientTLSCreds(cfg.CAFile, cfg.ServerName)
		if err != nil {
			return fmt.Errorf("build TLS creds: %w", err)
		}
	}

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if cfg.Token != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(tokenCreds{token: cfg.Token}))
	}
	conn, err := grpc.NewClient(cfg.Orchestrator, dialOpts...)
	if err != nil {
		return fmt.Errorf("dial orchestrator: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := shuttlev1.NewAgentServiceClient(conn)
	deployed := newDeployedSet()
	if n := deployed.seedFromDisk(cfg.WorkDir); n > 0 {
		slog.Info("reconciled deployed services from disk", "count", n, "work_dir", cfg.WorkDir)
	}

	var caddy *caddySidecar
	if cfg.CaddyEnabled {
		caddy = newCaddySidecar(CaddyOptions{DockerBin: cfg.DockerBin})
		if err := caddy.ensure(ctx); err != nil {
			slog.Error("caddy sidecar start failed; continuing without ingress", "err", err)
		} else {
			slog.Info("caddy ingress sidecar running", "network", caddy.opts.Network, "container", caddy.opts.Container)
		}
	}

	for {
		if err := runSession(ctx, cfg, client, driver, deployed, caddy); err != nil {
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

func runSession(ctx context.Context, cfg Config, client shuttlev1.AgentServiceClient, driver Driver, deployed *deployedSet, caddy *caddySidecar) error {
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

	// Container-state report goroutine: drives orchestrator drift detection.
	go reportStateLoop(ctx, stream, driver, deployed)

	for {
		cmd, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if err := handleCommand(ctx, cfg, stream, driver, deployed, caddy, cmd); err != nil {
			slog.Error("command error", "err", err)
		}
	}
}

func reportStateLoop(ctx context.Context, stream shuttlev1.AgentService_RegisterClient, driver Driver, deployed *deployedSet) {
	ticker := time.NewTicker(stateReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for service, ds := range deployed.snapshot() {
				status, err := driver.Status(ctx, service, ds.workDir)
				if err != nil {
					slog.Warn("status check failed", "service", service, "err", err)
					continue
				}
				if err := stream.Send(&shuttlev1.AgentEvent{
					Payload: &shuttlev1.AgentEvent_ContainerState{
						ContainerState: &shuttlev1.ContainerState{
							Service: service,
							Status:  status,
							Sha:     ds.sha,
						},
					},
				}); err != nil {
					return
				}
			}
		}
	}
}

func handleCommand(
	ctx context.Context,
	cfg Config,
	stream shuttlev1.AgentService_RegisterClient,
	driver Driver,
	deployed *deployedSet,
	caddy *caddySidecar,
	cmd *shuttlev1.OrchestratorCommand,
) error {
	switch payload := cmd.Payload.(type) {
	case *shuttlev1.OrchestratorCommand_Deploy:
		return executeDeploy(ctx, cfg, stream, driver, deployed, caddy, payload.Deploy)
	case *shuttlev1.OrchestratorCommand_Rollback:
		return executeRollback(ctx, cfg, stream, driver, deployed, caddy, payload.Rollback)
	case *shuttlev1.OrchestratorCommand_CaddyConfig:
		if caddy == nil {
			slog.Warn("received caddy config but --caddy is not enabled; ignoring")
			return nil
		}
		if err := caddy.apply(ctx, []byte(payload.CaddyConfig.ConfigJson)); err != nil {
			return fmt.Errorf("apply caddy config: %w", err)
		}
		slog.Info("caddy config applied")
		return nil
	case *shuttlev1.OrchestratorCommand_Teardown:
		return executeTeardown(ctx, cfg, stream, driver, deployed, payload.Teardown)
	default:
		slog.Warn("unknown command type", "type", fmt.Sprintf("%T", cmd.Payload))
	}
	return nil
}

func executeDeploy(ctx context.Context, cfg Config, stream shuttlev1.AgentService_RegisterClient, driver Driver, deployed *deployedSet, caddy *caddySidecar, req *shuttlev1.DeployRequest) error {
	slog.Info("executing deploy", "deploy_id", req.DeployId, "service", req.Service)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Apply(ctx, ApplyParams{
		Service:     req.Service,
		ComposeYAML: req.ComposeYaml,
		Env:         req.Env,
		WorkDir:     workDir,
	})
	if err == nil {
		deployed.put(req.Service, workDir, req.Sha)
	}
	res := streamDeployResult(stream, req.DeployId, logCh, err)
	connectToCaddy(ctx, caddy, workDir, req.Service, res)
	return res
}

func executeRollback(ctx context.Context, cfg Config, stream shuttlev1.AgentService_RegisterClient, driver Driver, deployed *deployedSet, caddy *caddySidecar, req *shuttlev1.RollbackRequest) error {
	slog.Info("executing rollback", "deploy_id", req.DeployId, "service", req.Service, "target_sha", req.TargetSha)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Rollback(ctx, RollbackParams{
		Service:     req.Service,
		ComposeYAML: req.ComposeYaml,
		Env:         req.Env,
		WorkDir:     workDir,
	})
	if err == nil {
		deployed.put(req.Service, workDir, req.TargetSha)
	}
	res := streamDeployResult(stream, req.DeployId, logCh, err)
	connectToCaddy(ctx, caddy, workDir, req.Service, res)
	return res
}

// executeTeardown brings a removed service down via the compose workspace on
// disk and stops tracking it. With remove_volumes set, named volumes are deleted
// and the workspace directory is removed; otherwise the workspace is kept so a
// later volume purge can still run `down --volumes` against it.
func executeTeardown(ctx context.Context, cfg Config, stream shuttlev1.AgentService_RegisterClient, driver Driver, deployed *deployedSet, req *shuttlev1.TeardownRequest) error {
	slog.Info("executing teardown", "deploy_id", req.DeployId, "service", req.Service, "remove_volumes", req.RemoveVolumes)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Down(ctx, req.Service, workDir, req.RemoveVolumes)
	if err == nil {
		deployed.remove(req.Service)
	}
	res := streamDeployResult(stream, req.DeployId, logCh, err)
	if res == nil && req.RemoveVolumes {
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			slog.Warn("remove workspace after teardown failed", "service", req.Service, "err", rmErr)
		}
	}
	return res
}

// connectToCaddy joins a freshly deployed project to the Caddy network so the
// sidecar can proxy to it. Best-effort: failures are logged, not fatal.
func connectToCaddy(ctx context.Context, caddy *caddySidecar, workDir, service string, deployErr error) {
	if caddy == nil || deployErr != nil {
		return
	}
	composePath := filepath.Join(workDir, "docker-compose.yml")
	if err := caddy.connectProject(ctx, composePath, service); err != nil {
		slog.Warn("caddy connect project failed", "service", service, "err", err)
	}
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

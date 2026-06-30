package agent

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/mtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const stateReportInterval = 30 * time.Second

// eventSink is the subset of the gRPC stream the agent uses to push events up.
// The concrete shuttlev1.AgentService_RegisterClient satisfies it directly; the
// session wraps it in an eventSender so concurrent senders are serialized.
type eventSink interface {
	Send(*shuttlev1.AgentEvent) error
}

// eventSender serializes AgentEvent sends. A gRPC stream does not support
// concurrent SendMsg, yet the agent sends from several goroutines at once — the
// heartbeat ticker, the container-state reporter, and command execution (which
// now also streams live deploy-log chunks). Every send goes through this mutex
// so they never interleave on the wire.
type eventSender struct {
	mu     sync.Mutex
	stream shuttlev1.AgentService_RegisterClient
}

func (s *eventSender) Send(ev *shuttlev1.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(ev)
}

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
	Token     string
	DockerBin string // docker executable, shared with the Caddy sidecar
	// CaddyImage overrides the Caddy sidecar image. Empty keeps the sidecar's own
	// default. DNS-challenge certificates require an image with the provider
	// plugin compiled in (the shipped ghcr.io/neikow/shuttle-caddy image).
	CaddyImage string
	// DNSImage overrides the CoreDNS sidecar image (private split-horizon DNS).
	// Empty keeps the default. The sidecar starts lazily, only when the
	// orchestrator pushes a DNS config to this host.
	DNSImage string
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

	caddy := newCaddySidecar(CaddyOptions{DockerBin: cfg.DockerBin, Image: cfg.CaddyImage})
	if err := caddy.ensure(ctx); err != nil {
		slog.Error("caddy sidecar start failed; continuing without ingress", "err", err)
	} else {
		slog.Info("caddy ingress sidecar running", "network", caddy.opts.Network, "container", caddy.opts.Container)
	}

	// CoreDNS sidecar is constructed but not started here — it starts lazily on
	// the first DNS config push (only the host a dns.yml sidecar provider names).
	dns := newDNSSidecar(DNSOptions{DockerBin: cfg.DockerBin, Image: cfg.DNSImage})

	for {
		if err := runSession(ctx, cfg, client, driver, deployed, caddy, dns); err != nil {
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

func runSession(ctx context.Context, cfg Config, client shuttlev1.AgentServiceClient, driver Driver, deployed *deployedSet, caddy *caddySidecar, dns *dnsSidecar) error {
	stream, err := client.Register(ctx)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	// All sends funnel through sender (serialized); Recv stays on the raw stream.
	sender := &eventSender{stream: stream}

	// Send registration.
	if err := sender.Send(&shuttlev1.AgentEvent{
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
				if err := sender.Send(&shuttlev1.AgentEvent{
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
	go reportStateLoop(ctx, sender, driver, deployed)

	for {
		cmd, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if err := handleCommand(ctx, cfg, sender, driver, deployed, caddy, dns, cmd); err != nil {
			slog.Error("command error", "err", err)
		}
	}
}

func reportStateLoop(ctx context.Context, stream eventSink, driver Driver, deployed *deployedSet) {
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
	stream eventSink,
	driver Driver,
	deployed *deployedSet,
	caddy *caddySidecar,
	dns *dnsSidecar,
	cmd *shuttlev1.OrchestratorCommand,
) error {
	switch payload := cmd.Payload.(type) {
	case *shuttlev1.OrchestratorCommand_Deploy:
		return executeDeploy(ctx, cfg, stream, driver, deployed, caddy, payload.Deploy)
	case *shuttlev1.OrchestratorCommand_Rollback:
		return executeRollback(ctx, cfg, stream, driver, deployed, caddy, payload.Rollback)
	case *shuttlev1.OrchestratorCommand_CaddyConfig:
		// Reconcile the sidecar's published ports first (recreates it when the
		// host's caddy ports changed) so the listen block in the pushed config
		// matches the container's port mapping.
		if err := caddy.reconcile(ctx, int(payload.CaddyConfig.HttpPort), int(payload.CaddyConfig.HttpsPort)); err != nil {
			return fmt.Errorf("reconcile caddy sidecar: %w", err)
		}
		if err := caddy.apply(ctx, []byte(payload.CaddyConfig.ConfigJson)); err != nil {
			return fmt.Errorf("apply caddy config: %w", err)
		}
		slog.Info("caddy config applied")
		return nil
	case *shuttlev1.OrchestratorCommand_DnsConfig:
		zones := make([]dnsZone, 0, len(payload.DnsConfig.Zones))
		for _, z := range payload.DnsConfig.Zones {
			zones = append(zones, dnsZone{Origin: z.Origin, Zonefile: z.Zonefile})
		}
		if err := dns.apply(ctx, zones, int(payload.DnsConfig.Port)); err != nil {
			return fmt.Errorf("apply dns config: %w", err)
		}
		slog.Info("dns sidecar config applied", "zones", len(zones))
		return nil
	case *shuttlev1.OrchestratorCommand_Teardown:
		return executeTeardown(ctx, cfg, stream, driver, deployed, payload.Teardown)
	case *shuttlev1.OrchestratorCommand_Backup:
		return executeBackup(ctx, cfg, stream, driver, payload.Backup)
	case *shuttlev1.OrchestratorCommand_Restore:
		return executeRestore(ctx, cfg, stream, driver, payload.Restore)
	default:
		slog.Warn("unknown command type", "type", fmt.Sprintf("%T", cmd.Payload))
	}
	return nil
}

func executeDeploy(ctx context.Context, cfg Config, stream eventSink, driver Driver, deployed *deployedSet, caddy *caddySidecar, req *shuttlev1.DeployRequest) error {
	slog.Info("executing deploy", "deploy_id", req.DeployId, "service", req.Service)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Apply(ctx, ApplyParams{
		Service:      req.Service,
		ComposeYAML:  req.ComposeYaml,
		Env:          req.Env,
		WorkDir:      workDir,
		UpdatePolicy: req.UpdatePolicy,
		// Rolling: join the new containers to the ingress network before the old
		// ones are removed, so Caddy keeps a healthy upstream throughout.
		OnNewContainers: func(ctx context.Context, ids []string) error {
			return caddy.connectContainers(ctx, ids, req.Service)
		},
	})
	if err == nil {
		deployed.put(req.Service, workDir, req.Sha)
	}
	res := streamDeployResult(stream, req.DeployId, req.Service, logCh, err)
	// Belt-and-suspenders for the recreate path (and any container the rolling
	// hook missed): ensure the live project is attached to the ingress network.
	connectToCaddy(ctx, caddy, workDir, req.Service, res)
	return res
}

func executeRollback(ctx context.Context, cfg Config, stream eventSink, driver Driver, deployed *deployedSet, caddy *caddySidecar, req *shuttlev1.RollbackRequest) error {
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
	res := streamDeployResult(stream, req.DeployId, req.Service, logCh, err)
	connectToCaddy(ctx, caddy, workDir, req.Service, res)
	return res
}

// executeTeardown brings a removed service down via the compose workspace on
// disk and stops tracking it. With remove_volumes set, named volumes are deleted
// and the workspace directory is removed; otherwise the workspace is kept so a
// later volume purge can still run `down --volumes` against it.
func executeTeardown(ctx context.Context, cfg Config, stream eventSink, driver Driver, deployed *deployedSet, req *shuttlev1.TeardownRequest) error {
	slog.Info("executing teardown", "deploy_id", req.DeployId, "service", req.Service, "remove_volumes", req.RemoveVolumes)
	workDir := filepath.Join(cfg.WorkDir, req.Service)

	logCh, err := driver.Down(ctx, req.Service, workDir, req.RemoveVolumes)
	if err == nil {
		deployed.remove(req.Service)
	}
	res := streamDeployResult(stream, req.DeployId, req.Service, logCh, err)
	if res == nil && req.RemoveVolumes {
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			slog.Warn("remove workspace after teardown failed", "service", req.Service, "err", rmErr)
		}
	}
	return res
}

// executeBackup runs a backup against the service's compose workspace on disk
// and reports the outcome (snapshot id, size, logs) back upstream.
func executeBackup(ctx context.Context, cfg Config, stream eventSink, driver Driver, req *shuttlev1.BackupRequest) error {
	slog.Info("executing backup", "backup_id", req.BackupId, "service", req.Service, "engine", req.Engine, "store", req.Store)
	workDir := filepath.Join(cfg.WorkDir, req.Service)
	logCh, doneCh, err := driver.Backup(ctx, BackupParams{
		BackupID:  req.BackupId,
		Service:   req.Service,
		Engine:    req.Engine,
		Store:     req.Store,
		Target:    req.Target,
		Env:       req.Env,
		WorkDir:   workDir,
		Volumes:   req.Volumes,
		DBService: req.DbService,
		DBUser:    req.DbUser,
		DBName:    req.DbName,
		Retention: retentionFromProto(req.Retention),
	})
	return streamBackupResult(stream, req.BackupId, "backup", req.Service, logCh, doneCh, err)
}

// executeRestore restores a prior backup into the service.
func executeRestore(ctx context.Context, cfg Config, stream eventSink, driver Driver, req *shuttlev1.RestoreRequest) error {
	slog.Info("executing restore", "operation_id", req.OperationId, "service", req.Service, "snapshot", req.SnapshotId)
	workDir := filepath.Join(cfg.WorkDir, req.Service)
	logCh, doneCh, err := driver.Restore(ctx, RestoreParams{
		OperationID: req.OperationId,
		Service:     req.Service,
		Engine:      req.Engine,
		Store:       req.Store,
		Target:      req.Target,
		SnapshotID:  req.SnapshotId,
		Env:         req.Env,
		WorkDir:     workDir,
		DBService:   req.DbService,
		DBUser:      req.DbUser,
		DBName:      req.DbName,
	})
	return streamBackupResult(stream, req.OperationId, "restore", req.Service, logCh, doneCh, err)
}

// retentionFromProto maps the proto retention into the driver's form.
func retentionFromProto(r *shuttlev1.BackupRetention) BackupRetention {
	if r == nil {
		return BackupRetention{}
	}
	return BackupRetention{
		KeepLast:    int(r.KeepLast),
		KeepDaily:   int(r.KeepDaily),
		KeepWeekly:  int(r.KeepWeekly),
		KeepMonthly: int(r.KeepMonthly),
	}
}

// streamBackupResult drains the backup/restore log stream and outcome, then sends
// one terminal BackupResult event. A start error (driver failed to launch) is
// reported as a failed result directly.
func streamBackupResult(stream eventSink, opID, operation, service string, logCh <-chan LogLine, doneCh <-chan BackupOutcome, startErr error) error {
	if startErr != nil {
		return stream.Send(&shuttlev1.AgentEvent{
			Payload: &shuttlev1.AgentEvent_BackupResult{
				BackupResult: &shuttlev1.BackupResult{
					OperationId: opID, Operation: operation, Service: service,
					Status: shuttlev1.BackupStatus_BACKUP_STATUS_FAILED, Error: startErr.Error(),
				},
			},
		})
	}

	var logs []*shuttlev1.LogLine
	for line := range logCh {
		logs = append(logs, &shuttlev1.LogLine{TsUnixMs: line.TsUnixMs, Stream: line.Stream, Text: line.Text})
	}
	out := <-doneCh

	status := shuttlev1.BackupStatus_BACKUP_STATUS_SUCCESS
	if out.Failed {
		status = shuttlev1.BackupStatus_BACKUP_STATUS_FAILED
	}
	return stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_BackupResult{
			BackupResult: &shuttlev1.BackupResult{
				OperationId: opID,
				Operation:   operation,
				Service:     service,
				Status:      status,
				SnapshotId:  out.SnapshotID,
				SizeBytes:   out.SizeBytes,
				Logs:        logs,
				Error:       out.Err,
			},
		},
	})
}

// connectToCaddy joins a freshly deployed project to the Caddy network so the
// sidecar can proxy to it. Best-effort: failures are logged, not fatal.
func connectToCaddy(ctx context.Context, caddy *caddySidecar, workDir, service string, deployErr error) {
	if deployErr != nil {
		return
	}
	composePath := filepath.Join(workDir, "docker-compose.yml")
	if err := caddy.connectProject(ctx, composePath, service); err != nil {
		slog.Warn("caddy connect project failed", "service", service, "err", err)
	}
}

// deployLogFlushInterval bounds how often partial log batches are flushed
// upstream, and deployLogBatchSize caps a single chunk. Together they keep the
// live tail responsive without sending one event per line.
const (
	deployLogFlushInterval = 500 * time.Millisecond
	deployLogBatchSize     = 32
)

func streamDeployResult(stream eventSink, deployID, service string, logCh <-chan LogLine, startErr error) error {
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

	// pending accumulates lines not yet streamed; flush sends them as one live
	// DeployLog chunk. Live streaming is best-effort — a send error is ignored
	// here because the terminal DeployResponse below (carrying the full logs) is
	// the authoritative, persisted record.
	var pending []*shuttlev1.LogLine
	flush := func() {
		if len(pending) == 0 {
			return
		}
		_ = stream.Send(&shuttlev1.AgentEvent{
			Payload: &shuttlev1.AgentEvent_DeployLog{
				DeployLog: &shuttlev1.DeployLog{DeployId: deployID, Service: service, Lines: pending},
			},
		})
		pending = nil
	}

	ticker := time.NewTicker(deployLogFlushInterval)
	defer ticker.Stop()
	open := true
	for open {
		select {
		case line, ok := <-logCh:
			if !ok {
				open = false
				break
			}
			ll := &shuttlev1.LogLine{TsUnixMs: line.TsUnixMs, Stream: line.Stream, Text: line.Text}
			logs = append(logs, ll)
			pending = append(pending, ll)
			if line.Stream == "stderr" && containsError(line.Text) {
				success = false
			}
			if len(pending) >= deployLogBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
	flush()

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

// containsError reports whether a log line carries the synthetic marker that
// emitErr writes when a compose operation fails.
func containsError(text string) bool {
	return strings.Contains(text, "[shuttle] compose error")
}

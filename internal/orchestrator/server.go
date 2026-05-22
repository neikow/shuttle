package orchestrator

import (
	"fmt"
	"io"
	"log/slog"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/ledger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentServiceServer implements the gRPC AgentService.
type AgentServiceServer struct {
	shuttlev1.UnimplementedAgentServiceServer
	registry *Registry
	store    *ledger.Store // optional; when nil, deploy results are not persisted
}

func NewAgentServiceServer(registry *Registry, store *ledger.Store) *AgentServiceServer {
	return &AgentServiceServer{registry: registry, store: store}
}

// ledgerStatus maps a proto DeployStatus to a ledger Status.
func ledgerStatus(s shuttlev1.DeployStatus) (ledger.Status, bool) {
	switch s {
	case shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS:
		return ledger.StatusSuccess, true
	case shuttlev1.DeployStatus_DEPLOY_STATUS_FAILED:
		return ledger.StatusFailed, true
	case shuttlev1.DeployStatus_DEPLOY_STATUS_ROLLED_BACK:
		return ledger.StatusRolledBack, true
	case shuttlev1.DeployStatus_DEPLOY_STATUS_RUNNING:
		return ledger.StatusRunning, true
	default:
		return "", false
	}
}

func (s *AgentServiceServer) Register(stream shuttlev1.AgentService_RegisterServer) error {
	// First message must be a RegisterRequest.
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first message must be RegisterRequest")
	}
	host := reg.Host
	if host == "" {
		return status.Error(codes.InvalidArgument, "host required")
	}

	conn := s.registry.register(host)
	defer s.registry.unregister(host)
	slog.Info("agent connected", "host", host, "version", reg.AgentVersion)

	// Fan-out: send commands from registry channel to stream.
	go func() {
		for cmd := range conn.send {
			if err := stream.Send(cmd); err != nil {
				slog.Error("send to agent failed", "host", host, "err", err)
				return
			}
		}
	}()

	// Receive loop: heartbeats + deploy results.
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			slog.Info("agent disconnected", "host", host)
			return nil
		}
		if err != nil {
			return fmt.Errorf("recv from %s: %w", host, err)
		}

		switch payload := msg.Payload.(type) {
		case *shuttlev1.AgentEvent_Heartbeat:
			s.registry.touch(host)
			slog.Debug("heartbeat", "host", host, "ts", payload.Heartbeat.TsUnixMs)
		case *shuttlev1.AgentEvent_DeployResult:
			res := payload.DeployResult
			slog.Info("deploy result",
				"host", host,
				"deploy_id", res.DeployId,
				"status", res.Status,
				"error", res.Error,
			)
			if s.store != nil {
				if ls, ok := ledgerStatus(res.Status); ok {
					if err := s.store.MarkStatus(stream.Context(), res.DeployId, ls); err != nil {
						slog.Error("mark deploy status", "deploy_id", res.DeployId, "err", err)
					}
				}
			}
		case *shuttlev1.AgentEvent_ContainerState:
			slog.Debug("container state",
				"host", host,
				"service", payload.ContainerState.Service,
				"status", payload.ContainerState.Status,
			)
			// TODO Phase 8: feed reconciler.
		}
	}
}

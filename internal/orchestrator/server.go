package orchestrator

import (
	"fmt"
	"io"
	"log/slog"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentServiceServer implements the gRPC AgentService.
type AgentServiceServer struct {
	shuttlev1.UnimplementedAgentServiceServer
	registry *Registry
}

func NewAgentServiceServer(registry *Registry) *AgentServiceServer {
	return &AgentServiceServer{registry: registry}
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
			slog.Info("deploy result",
				"host", host,
				"deploy_id", payload.DeployResult.DeployId,
				"status", payload.DeployResult.Status,
			)
			// TODO Phase 8: update ledger from deploy result.
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

package orchestrator

import (
	"fmt"
	"io"
	"log/slog"
	"time"

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
	tracker  *StateTracker // optional; when nil, container state is not tracked
	bus      *EventBus     // optional; nil-safe
	version  string        // orchestrator build version, for skew detection (optional)
}

func NewAgentServiceServer(registry *Registry, store *ledger.Store) *AgentServiceServer {
	return &AgentServiceServer{registry: registry, store: store}
}

// SetVersion records the orchestrator's own build version so Register can warn
// when a connecting agent reports a different version. Call before serving.
func (s *AgentServiceServer) SetVersion(v string) { s.version = v }

// SetStateTracker attaches a tracker that receives container state reports for
// drift detection. Call before serving.
func (s *AgentServiceServer) SetStateTracker(t *StateTracker) { s.tracker = t }

// SetEventBus attaches the event bus deploy results are published to. Call before serving.
func (s *AgentServiceServer) SetEventBus(b *EventBus) { s.bus = b }

// deployEventType maps a terminal deploy status to its event type, or false if
// the status is not a terminal result worth emitting.
func deployEventType(ls ledger.Status) (EventType, bool) {
	switch ls {
	case ledger.StatusSuccess:
		return EventDeploySucceeded, true
	case ledger.StatusFailed:
		return EventDeployFailed, true
	case ledger.StatusRolledBack:
		return EventDeployRolledBack, true
	default:
		return "", false
	}
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

// deployLogsFromProto maps the agent's reported log lines into ledger rows.
func deployLogsFromProto(in []*shuttlev1.LogLine) []ledger.DeployLog {
	if len(in) == 0 {
		return nil
	}
	out := make([]ledger.DeployLog, 0, len(in))
	for _, l := range in {
		if l == nil {
			continue
		}
		out = append(out, ledger.DeployLog{
			At:     time.UnixMilli(l.TsUnixMs),
			Stream: l.Stream,
			Text:   l.Text,
		})
	}
	return out
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
	// When token auth is in effect, the token is scoped to a host; reject a
	// register that claims a different one.
	if tokenHost, ok := tokenHostFromContext(stream.Context()); ok && tokenHost != host {
		return status.Errorf(codes.PermissionDenied, "token scoped to host %q, not %q", tokenHost, host)
	}

	conn := s.registry.register(host, reg.AgentVersion)
	defer s.registry.unregister(conn)
	slog.Info("agent connected", "host", host, "version", reg.AgentVersion)
	if s.version != "" && reg.AgentVersion != "" && reg.AgentVersion != s.version {
		slog.Warn("agent/orchestrator version skew",
			"host", host, "agent_version", reg.AgentVersion, "orchestrator_version", s.version)
	}

	// Fan-out: send commands from the registry channel to the stream until the
	// connection is retired (evicted, unregistered, or displaced by a reconnect).
	go func() {
		for {
			select {
			case <-conn.done:
				return
			case cmd := <-conn.send:
				if err := stream.Send(cmd); err != nil {
					slog.Error("send to agent failed", "host", host, "err", err)
					return
				}
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
			if ls, ok := ledgerStatus(res.Status); ok {
				if s.store != nil {
					if err := s.store.MarkStatus(stream.Context(), res.DeployId, ls); err != nil {
						slog.Error("mark deploy status", "deploy_id", res.DeployId, "err", err)
					}
					// Persist the captured output (best-effort: never gate the
					// deploy result on a log write).
					if logs := deployLogsFromProto(res.Logs); len(logs) > 0 {
						if err := s.store.RecordDeployLogs(stream.Context(), res.DeployId, logs); err != nil {
							slog.Error("record deploy logs", "deploy_id", res.DeployId, "err", err)
						}
					}
				}
				if et, emit := deployEventType(ls); emit {
					s.bus.Publish(Event{
						Type: et, Host: host, DeployID: res.DeployId,
						Status: string(ls), Message: res.Error,
					})
				}
			}
		case *shuttlev1.AgentEvent_ContainerState:
			cs := payload.ContainerState
			slog.Debug("container state",
				"host", host,
				"service", cs.Service,
				"status", cs.Status,
			)
			if s.tracker != nil {
				s.tracker.Record(host, cs.Service, cs.Status, cs.Sha)
			}
		}
	}
}

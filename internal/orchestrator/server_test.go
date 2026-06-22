package orchestrator

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/ledger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20

func newTestServer(t *testing.T) (*Registry, shuttlev1.AgentServiceClient) {
	reg, _, client := newTestServerWithLedger(t, nil)
	return reg, client
}

func newTestServerWithLedger(t *testing.T, store *ledger.Store) (*Registry, *ledger.Store, shuttlev1.AgentServiceClient) {
	t.Helper()
	reg := NewRegistry()
	srv := grpc.NewServer()
	shuttlev1.RegisterAgentServiceServer(srv, NewAgentServiceServer(reg, store))

	lis := bufconn.Listen(bufSize)
	t.Cleanup(func() {
		srv.Stop()
		lis.Close()
	})
	go srv.Serve(lis)

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return reg, store, shuttlev1.NewAgentServiceClient(conn)
}

func TestRegisterAndHeartbeat(t *testing.T) {
	reg, client := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Register(ctx)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Send register.
	if err := stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_Register{
			Register: &shuttlev1.RegisterRequest{Host: "web1", AgentVersion: "test"},
		},
	}); err != nil {
		t.Fatalf("send register: %v", err)
	}

	// Give server time to process.
	time.Sleep(50 * time.Millisecond)

	hosts := reg.ConnectedHosts()
	if len(hosts) != 1 || hosts[0] != "web1" {
		t.Errorf("expected web1 connected, got %v", hosts)
	}

	// Send heartbeat.
	if err := stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_Heartbeat{
			Heartbeat: &shuttlev1.Heartbeat{TsUnixMs: time.Now().UnixMilli()},
		},
	}); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	stream.CloseSend()
	time.Sleep(50 * time.Millisecond)

	if len(reg.ConnectedHosts()) != 0 {
		t.Error("expected agent evicted after disconnect")
	}
}

func TestDeployResult_UpdatesLedger(t *testing.T) {
	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctxBg := context.Background()
	if err := store.RecordDeploy(ctxBg, ledger.DeployRecord{
		DeployID: "dep-9", Service: "app", Host: "web1", SHA: "abc",
		Status: ledger.StatusPending, TriggeredBy: ledger.TriggeredByManual, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, _, client := newTestServerWithLedger(t, store)

	ctx, cancel := context.WithTimeout(ctxBg, 5*time.Second)
	defer cancel()
	stream, err := client.Register(ctx)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_Register{Register: &shuttlev1.RegisterRequest{Host: "web1"}},
	})
	stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_DeployResult{
			DeployResult: &shuttlev1.DeployResponse{
				DeployId: "dep-9", Status: shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS,
			},
		},
	})
	stream.CloseSend()

	// Poll the ledger for the status transition.
	var got ledger.Status
	for range 50 {
		recs, err := store.ListDeploys(ctxBg, "app", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 1 {
			got = recs[0].Status
			if got == ledger.StatusSuccess {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != ledger.StatusSuccess {
		t.Fatalf("expected ledger status success, got %q", got)
	}
}

func TestSendCommand(t *testing.T) {
	reg, client := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Register(ctx)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := stream.Send(&shuttlev1.AgentEvent{
		Payload: &shuttlev1.AgentEvent_Register{
			Register: &shuttlev1.RegisterRequest{Host: "web2"},
		},
	}); err != nil {
		t.Fatalf("send register: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	cmd := &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Deploy{
			Deploy: &shuttlev1.DeployRequest{DeployId: "dep-1", Service: "app"},
		},
	}
	if err := reg.Send("web2", cmd); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Agent should receive the command.
	received, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv command: %v", err)
	}
	d := received.GetDeploy()
	if d == nil || d.DeployId != "dep-1" {
		t.Errorf("unexpected command: %v", received)
	}
	stream.CloseSend()
}

func TestTwoAgents_killOne(t *testing.T) {
	reg, client := newTestServer(t)

	register := func(host string) shuttlev1.AgentService_RegisterClient {
		ctx := context.Background()
		stream, err := client.Register(ctx)
		if err != nil {
			t.Fatalf("Register %s: %v", host, err)
		}
		stream.Send(&shuttlev1.AgentEvent{
			Payload: &shuttlev1.AgentEvent_Register{
				Register: &shuttlev1.RegisterRequest{Host: host},
			},
		})
		return stream
	}

	s1 := register("host-a")
	s2 := register("host-b")
	time.Sleep(50 * time.Millisecond)

	if len(reg.ConnectedHosts()) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(reg.ConnectedHosts()))
	}

	s1.CloseSend()
	time.Sleep(50 * time.Millisecond)

	hosts := reg.ConnectedHosts()
	if len(hosts) != 1 || hosts[0] != "host-b" {
		t.Errorf("expected only host-b, got %v", hosts)
	}

	s2.CloseSend()
}

func TestHandleDeployLog_publishesEvent(t *testing.T) {
	bus := NewEventBus()
	sub, _ := bus.Subscribe()
	defer sub.Close()
	s := &AgentServiceServer{bus: bus}

	s.handleDeployLog("web1", &shuttlev1.DeployLog{
		DeployId: "dep-1",
		Service:  "api",
		Lines: []*shuttlev1.LogLine{
			{Stream: "stdout", Text: "pulling"},
			{Stream: "stdout", Text: "starting"},
		},
	})

	select {
	case ev := <-sub.C:
		if ev.Type != EventDeployLog {
			t.Errorf("type = %q, want deploy.log", ev.Type)
		}
		if ev.DeployID != "dep-1" || ev.Service != "api" || ev.Host != "web1" {
			t.Errorf("event = %+v, want dep-1/api/web1", ev)
		}
		if ev.Message != "pulling\nstarting" {
			t.Errorf("message = %q, want joined lines", ev.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("no deploy.log event published")
	}
}

func TestHandleDeployLog_emptyIsNoop(t *testing.T) {
	bus := NewEventBus()
	sub, _ := bus.Subscribe()
	defer sub.Close()
	s := &AgentServiceServer{bus: bus}

	s.handleDeployLog("web1", &shuttlev1.DeployLog{DeployId: "dep-1"}) // no lines
	s.handleDeployLog("web1", nil)

	select {
	case ev := <-sub.C:
		t.Fatalf("expected no event for an empty/nil log, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

package agent

import (
	"context"
	"net"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/orchestrator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestRunSession drives the agent run loop against a real orchestrator
// AgentServiceServer over an in-memory bufconn: the agent registers, the
// orchestrator pushes a deploy command, and the agent dispatches it to its
// (fake) driver — exercising runSession, Send, and the Recv→handleCommand path
// without Docker or a network.
func TestRunSession(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reg := orchestrator.NewRegistry()
	gs := grpc.NewServer()
	shuttlev1.RegisterAgentServiceServer(gs, orchestrator.NewAgentServiceServer(reg, store))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drv := &fakeDriver{}
	cfg := Config{Host: "web1", WorkDir: t.TempDir()}
	dns := newDNSSidecar(DNSOptions{DockerBin: stubBin(t, "exit 0")})
	sessionDone := make(chan struct{})
	go func() {
		_ = runSession(ctx, cfg, shuttlev1.NewAgentServiceClient(conn), drv, newDeployedSet(), okCaddy(t), dns)
		close(sessionDone)
	}()

	// Wait for the agent to register.
	waitUntil(t, 5*time.Second, func() bool {
		for _, h := range reg.Snapshot() {
			if h.Host == "web1" {
				return true
			}
		}
		return false
	})

	// Push a deploy; the agent should dispatch it to its driver.
	if err := reg.Send("web1", &shuttlev1.OrchestratorCommand{
		Payload: &shuttlev1.OrchestratorCommand_Deploy{Deploy: &shuttlev1.DeployRequest{
			DeployId: "d1", Service: "web", ComposeYaml: []byte("services: {}\n"),
		}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		for _, c := range drv.calls() {
			if c == "apply" {
				return true
			}
		}
		return false
	})

	cancel()
	select {
	case <-sessionDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runSession did not return after cancel")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

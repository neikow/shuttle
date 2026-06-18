package orchestrator

import (
	"sync"
	"testing"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

func newRegistryNoEvict() *Registry {
	return &Registry{conns: make(map[string]*agentConn)}
}

// A reconnect displaces the old connection; unregistering the old one must not
// remove or disturb the new one.
func TestRegistry_ReconnectThenStaleUnregister(t *testing.T) {
	r := newRegistryNoEvict()

	old := r.register("web1", "")
	select {
	case <-old.done:
		t.Fatal("new connection's done should be open")
	default:
	}

	fresh := r.register("web1", "") // reconnect displaces old
	select {
	case <-old.done:
	default:
		t.Fatal("displaced connection should be retired")
	}

	// The stale stream's deferred unregister fires after the reconnect.
	r.unregister(old)

	if got := r.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1 (fresh connection must survive)", got)
	}
	if err := r.Send("web1", &shuttlev1.OrchestratorCommand{}); err != nil {
		t.Fatalf("Send to fresh connection failed: %v", err)
	}
	select {
	case <-fresh.send:
	default:
		t.Fatal("command should have been enqueued on the fresh connection")
	}
}

// Send must never panic on a retired connection, and must report it as not
// connected.
func TestRegistry_SnapshotIncludesVersion(t *testing.T) {
	r := NewRegistry()
	r.register("web1", "v2.0.0")
	r.register("web2", "") // version unknown

	got := map[string]string{}
	for _, c := range r.Snapshot() {
		got[c.Host] = c.Version
	}
	if got["web1"] != "v2.0.0" {
		t.Errorf("web1 version = %q, want v2.0.0", got["web1"])
	}
	if got["web2"] != "" {
		t.Errorf("web2 version = %q, want empty", got["web2"])
	}
}

func TestRegistry_SendAfterUnregister(t *testing.T) {
	r := newRegistryNoEvict()
	conn := r.register("web1", "")
	r.unregister(conn)
	if err := r.Send("web1", &shuttlev1.OrchestratorCommand{}); err == nil {
		t.Fatal("Send after unregister should error")
	}
}

// Concurrent Send and unregister must not panic (was: send on closed channel).
func TestRegistry_ConcurrentSendUnregister(t *testing.T) {
	r := newRegistryNoEvict()
	for i := 0; i < 200; i++ {
		r.register("web1", "")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = r.Send("web1", &shuttlev1.OrchestratorCommand{})
		}()
		go func() {
			defer wg.Done()
			r.mu.RLock()
			conn := r.conns["web1"]
			r.mu.RUnlock()
			r.unregister(conn)
		}()
		wg.Wait()
	}
}

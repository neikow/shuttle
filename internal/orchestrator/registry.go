package orchestrator

import (
	"fmt"
	"sync"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

const (
	heartbeatInterval = 15 * time.Second
	evictAfter        = 45 * time.Second
)

type agentConn struct {
	host string
	send chan *shuttlev1.OrchestratorCommand
	// done is closed exactly once when this connection is retired (evicted,
	// unregistered, or displaced by a reconnect). The fan-out goroutine selects
	// on it to stop, and Send selects on it so it never writes to a retired
	// connection. The send channel is never closed, so a concurrent Send can
	// never panic on a closed channel.
	done      chan struct{}
	closeOnce sync.Once
	lastSeen  time.Time
}

func (c *agentConn) close() { c.closeOnce.Do(func() { close(c.done) }) }

// Registry tracks connected agents.
type Registry struct {
	mu    sync.RWMutex
	conns map[string]*agentConn
}

func NewRegistry() *Registry {
	r := &Registry{conns: make(map[string]*agentConn)}
	go r.evictLoop()
	return r
}

// register adds a connection for host and returns it. If host already has a
// connection (a stale stream that has not yet torn down), that one is retired
// first so its fan-out goroutine stops and it is no longer addressable.
func (r *Registry) register(host string) *agentConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.conns[host]; ok {
		old.close()
	}
	conn := &agentConn{
		host:     host,
		send:     make(chan *shuttlev1.OrchestratorCommand, 16),
		done:     make(chan struct{}),
		lastSeen: time.Now(),
	}
	r.conns[host] = conn
	return conn
}

// unregister retires conn and removes it from the registry, but only if it is
// still the current connection for its host. A connection that was already
// displaced by a reconnect is retired without disturbing its replacement.
func (r *Registry) unregister(conn *agentConn) {
	r.mu.Lock()
	if cur, ok := r.conns[conn.host]; ok && cur == conn {
		delete(r.conns, conn.host)
	}
	r.mu.Unlock()
	conn.close()
}

func (r *Registry) touch(host string) {
	r.mu.Lock()
	if conn, ok := r.conns[host]; ok {
		conn.lastSeen = time.Now()
	}
	r.mu.Unlock()
}

// Send enqueues a command to a specific agent.
func (r *Registry) Send(host string, cmd *shuttlev1.OrchestratorCommand) error {
	r.mu.RLock()
	conn, ok := r.conns[host]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %q not connected", host)
	}
	select {
	case <-conn.done:
		return fmt.Errorf("agent %q not connected", host)
	default:
	}
	select {
	case conn.send <- cmd:
		return nil
	case <-conn.done:
		return fmt.Errorf("agent %q not connected", host)
	default:
		return fmt.Errorf("agent %q send buffer full", host)
	}
}

// Count returns the number of currently connected agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// ConnectedHosts returns the list of currently connected host names.
func (r *Registry) ConnectedHosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.conns))
	for h := range r.conns {
		out = append(out, h)
	}
	return out
}

// HostConn is a connected agent's liveness snapshot.
type HostConn struct {
	Host     string    `json:"host"`
	LastSeen time.Time `json:"last_seen"`
}

// Snapshot returns the liveness of every connected agent.
func (r *Registry) Snapshot() []HostConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HostConn, 0, len(r.conns))
	for h, c := range r.conns {
		out = append(out, HostConn{Host: h, LastSeen: c.lastSeen})
	}
	return out
}

func (r *Registry) evictLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		for host, conn := range r.conns {
			if time.Since(conn.lastSeen) > evictAfter {
				conn.close()
				delete(r.conns, host)
			}
		}
		r.mu.Unlock()
	}
}

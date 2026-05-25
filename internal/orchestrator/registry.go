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
	host      string
	send      chan *shuttlev1.OrchestratorCommand
	lastSeen  time.Time
}

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

func (r *Registry) register(host string) *agentConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	conn := &agentConn{
		host:     host,
		send:     make(chan *shuttlev1.OrchestratorCommand, 16),
		lastSeen: time.Now(),
	}
	r.conns[host] = conn
	return conn
}

func (r *Registry) unregister(host string) {
	r.mu.Lock()
	if conn, ok := r.conns[host]; ok {
		close(conn.send)
		delete(r.conns, host)
	}
	r.mu.Unlock()
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
	case conn.send <- cmd:
		return nil
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

func (r *Registry) evictLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		for host, conn := range r.conns {
			if time.Since(conn.lastSeen) > evictAfter {
				close(conn.send)
				delete(r.conns, host)
			}
		}
		r.mu.Unlock()
	}
}

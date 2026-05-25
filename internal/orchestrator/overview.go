package orchestrator

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// OverviewHost is a host with its connection liveness and the services it is
// currently reporting (from the agent's periodic container-state reports).
type OverviewHost struct {
	Name      string         `json:"name"`
	Connected bool           `json:"connected"`
	LastSeen  *time.Time     `json:"last_seen,omitempty"`
	Services  []ServiceState `json:"services"`
}

// Overview is a single-screen snapshot of every known host and the health of the
// services running on it.
type Overview struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Hosts       []OverviewHost `json:"hosts"`
}

// SetStateTracker attaches the container-state tracker that powers GET /overview.
// Call before serving.
func (s *HTTPServer) SetStateTracker(t *StateTracker) { s.stateTracker = t }

// handleOverview merges connected-agent liveness (registry) with the latest
// reported container state per service (state tracker) into one view. A host
// appears if it is connected or has ever reported a service, so a crashed/offline
// host with known services still shows up (Connected=false).
func (s *HTTPServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	conns := map[string]time.Time{}
	if s.registry != nil {
		for _, c := range s.registry.Snapshot() {
			conns[c.Host] = c.LastSeen
		}
	}
	states := s.stateTracker.Snapshot() // nil-safe

	names := map[string]struct{}{}
	for h := range conns {
		names[h] = struct{}{}
	}
	for h := range states {
		names[h] = struct{}{}
	}

	hosts := make([]OverviewHost, 0, len(names))
	for name := range names {
		h := OverviewHost{Name: name, Services: states[name]}
		if h.Services == nil {
			h.Services = []ServiceState{}
		}
		if seen, ok := conns[name]; ok {
			h.Connected = true
			ls := seen
			h.LastSeen = &ls
		}
		hosts = append(hosts, h)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Overview{GeneratedAt: time.Now(), Hosts: hosts})
}

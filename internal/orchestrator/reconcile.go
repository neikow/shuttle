package orchestrator

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

// stateReport is the latest container state heard for a (host, service).
type stateReport struct {
	status string
	sha    string
	at     time.Time
}

// StateTracker records the most recent container state reported by agents and
// evaluates which desired services have drifted from running.
type StateTracker struct {
	mu  sync.RWMutex
	now func() time.Time
	// host -> service -> latest report
	byHost map[string]map[string]stateReport
}

func NewStateTracker() *StateTracker {
	return &StateTracker{now: time.Now, byHost: make(map[string]map[string]stateReport)}
}

// ServiceState is the latest reported container state for one service on a host.
type ServiceState struct {
	Service    string    `json:"service"`
	Status     string    `json:"status"`
	SHA        string    `json:"sha,omitempty"`
	ReportedAt time.Time `json:"reported_at"`
}

// Snapshot returns the latest reported service states grouped by host. Nil-safe:
// a nil tracker yields an empty map.
func (t *StateTracker) Snapshot() map[string][]ServiceState {
	out := map[string][]ServiceState{}
	if t == nil {
		return out
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for host, svcs := range t.byHost {
		list := make([]ServiceState, 0, len(svcs))
		for name, r := range svcs {
			list = append(list, ServiceState{Service: name, Status: r.status, SHA: r.sha, ReportedAt: r.at})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Service < list[j].Service })
		out[host] = list
	}
	return out
}

// Record stores the latest container state for a service on a host.
func (t *StateTracker) Record(host, service, status, sha string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	svcs := t.byHost[host]
	if svcs == nil {
		svcs = make(map[string]stateReport)
		t.byHost[host] = svcs
	}
	svcs[service] = stateReport{status: status, sha: sha, at: t.now()}
}

// DriftedServices returns the names of desired services whose latest report is
// stale or not running. Services with no report at all are skipped (unknown,
// not drifted) to avoid redeploy storms on startup. SHA-based drift is handled
// separately by GitSyncer.Reconcile.
func (t *StateTracker) DriftedServices(repo *config.Repo, staleAfter time.Duration) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := t.now()
	var drifted []string
	for _, svc := range repo.Services {
		rep, ok := t.byHost[svc.Host][svc.Name]
		if !ok {
			continue
		}
		if now.Sub(rep.at) > staleAfter || !isRunning(rep.status) {
			drifted = append(drifted, svc.Name)
		}
	}
	return drifted
}

// isRunning reports whether a docker compose status string indicates a healthy,
// running container.
func isRunning(status string) bool {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "running"), strings.Contains(s, "up"), strings.Contains(s, "healthy"):
		return true
	default:
		return false
	}
}

// DriftReconciler periodically re-syncs the repo (catching new commits and
// failed deploys via the ledger) and force-redeploys services whose containers
// have crashed (detected via the StateTracker).
type DriftReconciler struct {
	syncer     *GitSyncer
	tracker    *StateTracker
	interval   time.Duration
	staleAfter time.Duration
	bus        *EventBus // optional; nil-safe
}

func NewDriftReconciler(syncer *GitSyncer, tracker *StateTracker, interval, staleAfter time.Duration) *DriftReconciler {
	return &DriftReconciler{syncer: syncer, tracker: tracker, interval: interval, staleAfter: staleAfter}
}

// SetEventBus attaches the event bus drift events are published to. Call before Run.
func (d *DriftReconciler) SetEventBus(b *EventBus) { d.bus = b }

// Run ticks until ctx is cancelled.
func (d *DriftReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *DriftReconciler) tick(ctx context.Context) {
	// SHA drift: new commits or previously-failed deploys. Also detects services
	// removed from the repo and tears them down (reconcileRemovals).
	if _, err := d.syncer.Reconcile(ctx, nil); err != nil {
		slog.Error("reconcile (sha drift) failed", "err", err)
		return
	}
	// Volume purge: delete volumes of removed services whose deadline passed.
	if purged, err := d.syncer.PurgeExpiredVolumes(ctx); err != nil {
		slog.Error("purge expired volumes failed", "err", err)
	} else if len(purged) > 0 {
		slog.Info("purged volumes of removed services", "services", purged)
	}
	// Container drift: desired services whose containers have crashed.
	repo, err := config.Load(d.syncer.LocalDir())
	if err != nil {
		slog.Error("reconcile (container drift) load config", "err", err)
		return
	}
	drifted := d.tracker.DriftedServices(repo, d.staleAfter)
	if len(drifted) == 0 {
		return
	}
	slog.Warn("container drift detected, redeploying", "services", drifted)
	for _, svc := range drifted {
		d.bus.Publish(Event{
			Type: EventDriftDetected, Service: svc,
			Message: "container not running; redeploying",
		})
	}
	// The heal itself is dispatched via ForceDeploy → dispatch, which publishes
	// the resulting deploy.queued events.
	if _, err := d.syncer.ForceDeploy(ctx, drifted); err != nil {
		slog.Error("force redeploy failed", "services", drifted, "err", err)
	}
}

package orchestrator

import (
	"context"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics subscribes to the event bus and exposes orchestrator activity as
// Prometheus metrics. It keeps its own registry (not the global default) so the
// exposed surface is exactly what we register. Metric labels are deliberately
// low-cardinality — event type only, never service or host names — so /metrics
// can be scraped unauthenticated without leaking topology.
type Metrics struct {
	reg      *prometheus.Registry
	events   *prometheus.CounterVec
	duration prometheus.Histogram

	mu      sync.Mutex
	started map[string]float64 // deploy ID -> queued time (unix seconds)
}

// NewMetrics builds the collector, registering a gauge for connected agents and
// a counter for dropped bus events that read live from registry and bus at
// scrape time. Call Run to start consuming events.
func NewMetrics(bus *EventBus, registry *Registry) *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shuttle_events_total",
			Help: "Orchestrator events emitted, by type.",
		}, []string{"type"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "shuttle_deploy_duration_seconds",
			Help:    "Time from a deploy/rollback being queued to its terminal result.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s .. ~512s
		}),
		started: make(map[string]float64),
	}
	m.reg.MustRegister(m.events, m.duration)
	m.reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "shuttle_connected_agents",
		Help: "Number of agents currently connected.",
	}, func() float64 { return float64(registry.Count()) }))
	m.reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "shuttle_event_bus_dropped_total",
		Help: "Events dropped because a subscriber's buffer was full.",
	}, func() float64 { return float64(bus.Dropped()) }))
	return m
}

// Handler returns the /metrics HTTP handler for this collector's registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Run subscribes to the bus and records metrics until ctx is cancelled. It
// first folds in the replay backlog, so any events buffered before the
// collector attached are counted exactly once.
func (m *Metrics) Run(ctx context.Context, bus *EventBus) {
	sub, backlog := bus.Subscribe()
	defer sub.Close()
	for _, ev := range backlog {
		m.record(ev)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.C:
			if !open {
				return
			}
			m.record(ev)
		}
	}
}

// record updates the counters and, for deploy lifecycle events, the duration
// histogram. A queued event stores its timestamp keyed by deploy ID; the
// matching terminal event observes the elapsed time and clears the entry.
func (m *Metrics) record(ev Event) {
	m.events.WithLabelValues(string(ev.Type)).Inc()
	if ev.DeployID == "" {
		return
	}
	switch ev.Type {
	case EventDeployQueued, EventRollbackQueued:
		m.mu.Lock()
		m.started[ev.DeployID] = float64(ev.Time.UnixNano()) / 1e9
		m.mu.Unlock()
	case EventDeploySucceeded, EventDeployFailed, EventDeployRolledBack:
		m.mu.Lock()
		start, ok := m.started[ev.DeployID]
		delete(m.started, ev.DeployID)
		m.mu.Unlock()
		if ok {
			if d := float64(ev.Time.UnixNano())/1e9 - start; d >= 0 {
				m.duration.Observe(d)
			}
		}
	}
}

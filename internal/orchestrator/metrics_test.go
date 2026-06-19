package orchestrator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrapeMetrics(t *testing.T, m *Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestMetrics_RecordsEventsAndDuration(t *testing.T) {
	m := NewMetrics(NewEventBus(), NewRegistry())
	t0 := time.Unix(1000, 0)
	m.record(Event{Type: EventDeployQueued, DeployID: "d1", Time: t0})
	m.record(Event{Type: EventDeploySucceeded, DeployID: "d1", Time: t0.Add(5 * time.Second)})

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`shuttle_events_total{type="deploy.queued"} 1`,
		`shuttle_events_total{type="deploy.succeeded"} 1`,
		`shuttle_deploy_duration_seconds_count 1`,
		`shuttle_deploy_duration_seconds_sum 5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestMetrics_ConnectedAgentsGauge(t *testing.T) {
	reg := NewRegistry()
	m := NewMetrics(NewEventBus(), reg)
	reg.register("h1", "")
	reg.register("h2", "")

	if body := scrapeMetrics(t, m); !strings.Contains(body, "shuttle_connected_agents 2") {
		t.Errorf("want connected_agents 2\n---\n%s", body)
	}
}

func TestMetrics_DroppedCounter(t *testing.T) {
	bus := NewEventBus()
	bus.subBuffer = 1
	sub, _ := bus.Subscribe() // never drained
	defer sub.Close()
	bus.Publish(Event{Type: EventDeployQueued})
	bus.Publish(Event{Type: EventDeployQueued})
	bus.Publish(Event{Type: EventDeployQueued})

	m := NewMetrics(bus, NewRegistry())
	if body := scrapeMetrics(t, m); !strings.Contains(body, "shuttle_event_bus_dropped_total 2") {
		t.Errorf("want dropped_total 2\n---\n%s", body)
	}
}

func TestMetrics_RunConsumesBus(t *testing.T) {
	bus := NewEventBus()
	m := NewMetrics(bus, NewRegistry())
	go m.Run(t.Context(), bus)

	bus.Publish(Event{Type: EventServiceRemoved, Service: "old"})

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(scrapeMetrics(t, m), `shuttle_events_total{type="service.removed"} 1`) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("metric not recorded from bus within timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

package orchestrator

import (
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

func testRepo() *config.Repo {
	return &config.Repo{
		Hosts: []config.Host{{Name: "web1"}},
		Services: []config.Service{
			{Name: "app", Host: "web1"},
			{Name: "api", Host: "web1"},
		},
	}
}

func TestStateTracker_DriftedServices(t *testing.T) {
	tr := NewStateTracker()
	now := time.Now()
	tr.now = func() time.Time { return now }

	tr.Record("web1", "app", "running", "sha1") // healthy
	tr.Record("web1", "api", "exited", "sha1")  // crashed
	// "web1"/unknown service has no report.

	drifted := tr.DriftedServices(testRepo(), time.Minute)
	if len(drifted) != 1 || drifted[0] != "api" {
		t.Fatalf("expected [api], got %v", drifted)
	}
}

func TestStateTracker_StaleIsDrift(t *testing.T) {
	tr := NewStateTracker()
	base := time.Now()
	tr.now = func() time.Time { return base }
	tr.Record("web1", "app", "running", "sha1")

	// Advance time past the staleness window.
	tr.now = func() time.Time { return base.Add(2 * time.Minute) }
	drifted := tr.DriftedServices(testRepo(), time.Minute)
	if len(drifted) != 1 || drifted[0] != "app" {
		t.Fatalf("expected [app] stale, got %v", drifted)
	}
}

func TestStateTracker_MissingReportSkipped(t *testing.T) {
	tr := NewStateTracker()
	// No reports recorded at all → nothing drifted (avoids startup storms).
	if d := tr.DriftedServices(testRepo(), time.Minute); len(d) != 0 {
		t.Fatalf("expected no drift for unknown services, got %v", d)
	}
}

func TestIsRunning(t *testing.T) {
	cases := map[string]bool{
		"running": true, "Up 3 minutes": true, "healthy": true,
		"exited": false, "dead": false, "stopped": false, "": false,
	}
	for status, want := range cases {
		if got := isRunning(status); got != want {
			t.Errorf("isRunning(%q) = %v, want %v", status, got, want)
		}
	}
}

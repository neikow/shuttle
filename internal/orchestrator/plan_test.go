package orchestrator

import (
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestBuildPlanReport(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{
		{Name: "web", Host: "h1"},
		{Name: "api", Host: "h1"},
		{Name: "worker", Host: "h2"},
	}}
	current := map[string]string{
		"web":   "oldsha", // deployed at a different SHA → update
		"api":   "newsha", // deployed at desired SHA → unchanged
		"ghost": "zzz",    // in ledger, not in repo → remove
		// worker has no ledger entry → create
	}

	report := buildPlanReport(repo, current, "newsha")
	if report.SHA != "newsha" {
		t.Fatalf("SHA = %q", report.SHA)
	}

	// Sorted by service name: api, ghost, web, worker.
	want := []struct {
		service string
		action  PlanAction
	}{
		{"api", PlanUnchanged},
		{"ghost", PlanRemove},
		{"web", PlanUpdate},
		{"worker", PlanCreate},
	}
	if len(report.Services) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(report.Services), len(want), report.Services)
	}
	for i, w := range want {
		got := report.Services[i]
		if got.Service != w.service || got.Action != w.action {
			t.Errorf("entry %d = (%s, %s), want (%s, %s)", i, got.Service, got.Action, w.service, w.action)
		}
	}
}

func TestBuildPlanReport_NoLedgerIsAllCreate(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{
		{Name: "web", Host: "h1"},
		{Name: "api", Host: "h1"},
	}}
	report := buildPlanReport(repo, map[string]string{}, "sha1")
	for _, e := range report.Services {
		if e.Action != PlanCreate {
			t.Errorf("%s: action = %s, want create", e.Service, e.Action)
		}
	}
}

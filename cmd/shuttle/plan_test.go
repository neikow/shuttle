package main

import (
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/orchestrator"
)

func TestRenderPlan(t *testing.T) {
	report := orchestrator.PlanReport{
		SHA: "abcdef1234567890",
		Services: []orchestrator.PlanEntry{
			{Service: "api", Host: "h1", Action: orchestrator.PlanUnchanged},
			{Service: "ghost", Action: orchestrator.PlanRemove, CurrentSHA: "0011223344556677"},
			{Service: "web", Host: "h1", Action: orchestrator.PlanUpdate, CurrentSHA: "1111111111111111", DesiredSHA: "abcdef1234567890"},
			{Service: "worker", Host: "h2", Action: orchestrator.PlanCreate},
		},
	}

	var out strings.Builder
	changes := renderPlan(&out, report)
	if changes != 3 {
		t.Fatalf("changes = %d, want 3 (create+update+remove)", changes)
	}

	got := out.String()
	for _, want := range []string{
		"Plan against abcdef123456:",
		"+ create    worker (host=h2)",
		"~ update    web (host=h1)  111111111111 → abcdef123456",
		"- remove    ghost  001122334455 → (gone)",
		"1 to create, 1 to update, 1 to remove, 1 unchanged.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderPlan_Empty(t *testing.T) {
	var out strings.Builder
	if changes := renderPlan(&out, orchestrator.PlanReport{SHA: "x"}); changes != 0 {
		t.Fatalf("changes = %d, want 0", changes)
	}
	if !strings.Contains(out.String(), "(no services)") {
		t.Errorf("expected '(no services)', got:\n%s", out.String())
	}
}

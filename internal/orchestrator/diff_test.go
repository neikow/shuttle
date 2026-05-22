package orchestrator

import (
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestComputePlan_allDeploy(t *testing.T) {
	repo := &config.Repo{
		Hosts: []config.Host{{Name: "web1"}},
		Services: []config.Service{
			{Name: "app", Host: "web1", Source: config.LocalCompose{Path: "services/app/docker-compose.yml"}},
		},
	}
	plan := ComputePlan(repo, CurrentState{}, "abc123")
	if len(plan.Steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(plan.Steps))
	}
	s := plan.Steps[0]
	if s.Action != ActionDeploy || s.Service != "app" || s.SHA != "abc123" {
		t.Errorf("unexpected step: %+v", s)
	}
}

func TestComputePlan_noop(t *testing.T) {
	repo := &config.Repo{
		Hosts: []config.Host{{Name: "web1"}},
		Services: []config.Service{
			{Name: "app", Host: "web1", Source: config.LocalCompose{Path: "services/app/docker-compose.yml"}},
		},
	}
	plan := ComputePlan(repo, CurrentState{"app": "abc123"}, "abc123")
	if len(plan.Steps) != 0 {
		t.Errorf("want 0 steps (noop), got %d", len(plan.Steps))
	}
}

func TestComputePlan_partialUpdate(t *testing.T) {
	repo := &config.Repo{
		Hosts: []config.Host{{Name: "web1"}},
		Services: []config.Service{
			{Name: "app", Host: "web1", Source: config.LocalCompose{Path: "a"}},
			{Name: "api", Host: "web1", Source: config.LocalCompose{Path: "b"}},
		},
	}
	// app is current, api is stale.
	plan := ComputePlan(repo, CurrentState{"app": "new", "api": "old"}, "new")
	if len(plan.Steps) != 1 || plan.Steps[0].Service != "api" {
		t.Errorf("expected only api to deploy, got %+v", plan.Steps)
	}
}

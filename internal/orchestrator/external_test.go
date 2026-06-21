package orchestrator

import (
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func externalSvc(name, host, domain, upstream string) config.Service {
	return config.Service{
		Name: name, Host: host, Domains: []string{domain},
		Source: config.ExternalService{Upstream: upstream},
	}
}

func TestRoutes_externalUpstreamVerbatim(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{
		{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Port: 8080},
		externalSvc("infisical", "web1", "infisical.example.com", "infisical:8080"),
	}}

	// Host sidecar: managed dials the alias; external dials its upstream verbatim.
	host, err := RoutesForHost(repo, "web1")
	if err != nil {
		t.Fatalf("RoutesForHost: %v", err)
	}
	got := map[string]string{}
	for _, r := range host {
		got[r.Domain] = r.Upstream
	}
	if got["app.example.com"] != "app:8080" {
		t.Errorf("managed upstream = %q, want app:8080", got["app.example.com"])
	}
	if got["infisical.example.com"] != "infisical:8080" {
		t.Errorf("external upstream = %q, want infisical:8080 (verbatim)", got["infisical.example.com"])
	}

	// Central Caddy: managed dials host:port; external still verbatim.
	central, err := RoutesFromRepo(repo)
	if err != nil {
		t.Fatalf("RoutesFromRepo: %v", err)
	}
	got = map[string]string{}
	for _, r := range central {
		got[r.Domain] = r.Upstream
	}
	if got["app.example.com"] != "web1:8080" {
		t.Errorf("central managed upstream = %q, want web1:8080", got["app.example.com"])
	}
	if got["infisical.example.com"] != "infisical:8080" {
		t.Errorf("central external upstream = %q, want infisical:8080", got["infisical.example.com"])
	}
}

func TestComputePlan_skipsExternal(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{
		{Name: "app", Host: "web1", Source: config.LocalCompose{Path: "x"}},
		externalSvc("infisical", "web1", "infisical.example.com", "infisical:8080"),
	}}
	plan := ComputePlan(repo, CurrentState{}, "sha1")
	if len(plan.Steps) != 1 || plan.Steps[0].Service != "app" {
		t.Fatalf("want only app deployed, got %+v", plan.Steps)
	}
}

func TestBuildPlanReport_skipsExternal(t *testing.T) {
	repo := &config.Repo{Services: []config.Service{
		{Name: "app", Host: "web1", Source: config.LocalCompose{Path: "x"}},
		externalSvc("infisical", "web1", "infisical.example.com", "infisical:8080"),
	}}
	report := buildPlanReport(repo, map[string]string{}, "sha1")
	for _, e := range report.Services {
		if e.Service == "infisical" {
			t.Fatalf("external service should not appear in the plan: %+v", e)
		}
	}
	if len(report.Services) != 1 {
		t.Fatalf("want 1 plan entry (app), got %d", len(report.Services))
	}
}

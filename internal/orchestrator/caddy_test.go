package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestRoutesFromRepo(t *testing.T) {
	repo := &config.Repo{
		Services: []config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com", "www.example.com"},
				Healthcheck: &config.Healthcheck{Port: 8080}},
			{Name: "nodomains", Host: "web1", Healthcheck: &config.Healthcheck{Port: 80}}, // skipped: no domains
			{Name: "noport", Host: "web1", Domains: []string{"x.com"}},                    // skipped: no healthcheck
		},
	}
	routes, err := RoutesFromRepo(repo)
	if err != nil {
		t.Fatalf("RoutesFromRepo: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}
	for _, r := range routes {
		if r.Upstream != "web1:8080" {
			t.Errorf("upstream = %q, want web1:8080", r.Upstream)
		}
	}
}

func TestRoutesFromRepo_snippet(t *testing.T) {
	repo := &config.Repo{
		Services: []config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com"},
				Healthcheck:  &config.Healthcheck{Port: 8080},
				CaddySnippet: `[{"handler":"headers","response":{"set":{"X-Foo":["bar"]}}}]`},
		},
	}
	routes, err := RoutesFromRepo(repo)
	if err != nil {
		t.Fatalf("RoutesFromRepo: %v", err)
	}
	if len(routes) != 1 || len(routes[0].Handlers) != 1 {
		t.Fatalf("want 1 route with 1 snippet handler, got %+v", routes)
	}

	cfg := buildCaddyConfig(routes)
	srv := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["shuttle"].(map[string]any)
	route0 := srv["routes"].([]any)[0].(map[string]any)
	handle := route0["handle"].([]any)
	if len(handle) != 2 {
		t.Fatalf("want 2 handlers (snippet + proxy), got %d: %+v", len(handle), handle)
	}
	if h := handle[0].(map[string]any)["handler"]; h != "headers" {
		t.Errorf("first handler = %v, want headers (snippet before proxy)", h)
	}
	if h := handle[1].(map[string]any)["handler"]; h != "reverse_proxy" {
		t.Errorf("last handler = %v, want reverse_proxy", h)
	}
}

func TestRoutesFromRepo_badSnippet(t *testing.T) {
	repo := &config.Repo{
		Services: []config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com"},
				Healthcheck: &config.Healthcheck{Port: 8080}, CaddySnippet: `{not valid`},
		},
	}
	if _, err := RoutesFromRepo(repo); err == nil {
		t.Fatal("expected error on invalid snippet JSON, got nil")
	}
}

func TestRoutesForHost(t *testing.T) {
	repo := &config.Repo{
		Services: []config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Healthcheck: &config.Healthcheck{Port: 8080}},
			{Name: "api", Host: "web2", Domains: []string{"api.example.com"}, Healthcheck: &config.Healthcheck{Port: 80}},
		},
	}
	routes, err := RoutesForHost(repo, "web1")
	if err != nil {
		t.Fatalf("RoutesForHost: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 route for web1, got %d", len(routes))
	}
	// Upstream dials the service NAME (network alias), not the host.
	if routes[0].Upstream != "app:8080" {
		t.Errorf("upstream = %q, want app:8080", routes[0].Upstream)
	}
}

func TestHostCaddyConfigJSON(t *testing.T) {
	repo := &config.Repo{
		Services: []config.Service{
			{Name: "app", Host: "web1", Domains: []string{"app.example.com"}, Healthcheck: &config.Healthcheck{Port: 8080}},
			{Name: "lonely", Host: "web3"}, // no domains -> web3 has no config
		},
	}

	data, ok, err := HostCaddyConfigJSON(repo, "web1")
	if err != nil || !ok {
		t.Fatalf("web1: ok=%v err=%v", ok, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, hasApps := cfg["apps"]; !hasApps {
		t.Errorf("config missing apps: %s", data)
	}

	if _, ok, _ := HostCaddyConfigJSON(repo, "web3"); ok {
		t.Error("web3 has no routable services; expected ok=false")
	}
}

func TestApplyRoutes(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewCaddyClient(srv.URL)
	routes := []CaddyRoute{
		{Domain: "app.example.com", Upstream: "localhost:8080"},
	}
	if err := client.ApplyRoutes(context.Background(), routes); err != nil {
		t.Fatalf("ApplyRoutes: %v", err)
	}
	if received == nil {
		t.Fatal("no config received by mock Caddy")
	}
	apps, ok := received["apps"].(map[string]any)
	if !ok {
		t.Fatalf("missing apps: %v", received)
	}
	if _, ok := apps["http"]; !ok {
		t.Error("missing http app in config")
	}
}

func TestApplyRoutes_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := NewCaddyClient(srv.URL)
	err := client.ApplyRoutes(context.Background(), []CaddyRoute{{Domain: "x.com", Upstream: "localhost:80"}})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

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
	routes := RoutesFromRepo(repo)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}
	for _, r := range routes {
		if r.Upstream != "web1:8080" {
			t.Errorf("upstream = %q, want web1:8080", r.Upstream)
		}
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

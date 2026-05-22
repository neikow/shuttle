package orchestrator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	if err := client.ApplyRoutes(routes); err != nil {
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
	err := client.ApplyRoutes([]CaddyRoute{{Domain: "x.com", Upstream: "localhost:80"}})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

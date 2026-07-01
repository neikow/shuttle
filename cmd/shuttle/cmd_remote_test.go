package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/spf13/cobra"
)

// flagCmd builds a bare cobra.Command with the given string flags pre-set to
// their values and a non-nil context, so the RunE/helper functions can read
// flags and build context-aware requests.
func flagCmd(kv map[string]string) *cobra.Command {
	c := &cobra.Command{}
	for k, v := range kv {
		c.Flags().String(k, v, "")
	}
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetContext(context.Background())
	return c
}

func TestWebhookCreateListDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/webhooks/repo":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"abc123"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/webhooks/repo":
			_, _ = w.Write([]byte(`[{"ID":"abc123","Service":"web","CreatedAt":"2026-01-01T00:00:00Z"}]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/webhooks/repo/abc123":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := runWebhookCreate(flagCmd(map[string]string{"url": srv.URL, "token": "t", "service": "web", "base-url": ""}), nil); err != nil {
		t.Errorf("create: %v", err)
	}
	if err := runWebhookList(flagCmd(map[string]string{"url": srv.URL, "token": "t"}), nil); err != nil {
		t.Errorf("list: %v", err)
	}
	if err := runWebhookDelete(flagCmd(map[string]string{"url": srv.URL, "token": "t"}), []string{"abc123"}); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := runWebhookDelete(flagCmd(map[string]string{"url": srv.URL, "token": "t"}), []string{"missing"}); err == nil {
		t.Error("delete of unknown id should error")
	}
}

func TestCheckRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"abc","has_provider":true,"services":[{"service":"web","schema":["A"]}]}`))
	}))
	defer srv.Close()

	rep, err := checkRemote(flagCmd(map[string]string{"token": "t", "ref": "main"}), srv.URL)
	if err != nil || rep == nil || rep.SHA != "abc" {
		t.Fatalf("checkRemote = %+v err=%v", rep, err)
	}
	// Missing token -> error.
	if _, err := checkRemote(flagCmd(map[string]string{}), srv.URL); err == nil {
		t.Error("checkRemote without token should error")
	}
}

func TestPlanRemoteAndRender(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"deadbeef","services":[
			{"action":"create","service":"new","host":"h1"},
			{"action":"update","service":"web","host":"h1","current_sha":"aaa","desired_sha":"bbb"},
			{"action":"remove","service":"old","current_sha":"ccc"},
			{"action":"unchanged","service":"db"}
		]}`))
	}))
	defer srv.Close()

	rep, err := planRemote(flagCmd(map[string]string{"token": "t"}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	changes := renderPlan(&buf, rep)
	if changes != 3 {
		t.Errorf("changes = %d, want 3 (create+update+remove)", changes)
	}
	out := buf.String()
	for _, want := range []string{"+ create", "~ update", "- remove", "1 to create, 1 to update, 1 to remove, 1 unchanged"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderPlan missing %q in:\n%s", want, out)
		}
	}

	// Empty plan renders "(no services)".
	var empty bytes.Buffer
	renderPlan(&empty, orchestrator.PlanReport{SHA: "x"})
	if !strings.Contains(empty.String(), "(no services)") {
		t.Errorf("empty plan should say (no services): %s", empty.String())
	}
}

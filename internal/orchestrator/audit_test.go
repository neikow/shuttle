package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/ledger"
)

func TestListAudit_unauthorized(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/audit", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestListAudit_empty(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/audit"))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("want empty JSON array, got %q", got)
	}
}

// TestAudit_recordsWebhookCreate exercises the full record → list path through a
// real handler: creating a repo webhook must leave a webhook.create audit entry,
// and an X-Actor header must be honored as the actor.
func TestAudit_recordsWebhookCreate(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/repo", strings.NewReader(`{"service":"api"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "ci-bot")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create webhook: want 201, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/audit?action=webhook.create"))
	if w.Code != http.StatusOK {
		t.Fatalf("list audit: want 200, got %d", w.Code)
	}
	var entries []ledger.AuditEntry
	if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != auditWebhookCreate {
		t.Errorf("action = %q, want %q", e.Action, auditWebhookCreate)
	}
	if e.Actor != "ci-bot" {
		t.Errorf("actor = %q, want ci-bot (from X-Actor)", e.Actor)
	}
	if e.Target != "api" {
		t.Errorf("target = %q, want api", e.Target)
	}
	if e.Result != auditSuccess {
		t.Errorf("result = %q, want %q", e.Result, auditSuccess)
	}
}

// TestAudit_recordsDeployFailure proves the failure path is audited: a legacy
// deploy to a host with no connected agent fails to send and must leave a
// deploy/failure entry, defaulting the actor to "operator" when no X-Actor is
// set.
func TestAudit_recordsDeployFailure(t *testing.T) {
	srv := newHTTPTestServer(t)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/deploy/myapp?sha=abc123&host=web1"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("deploy: want 502, got %d: %s", w.Code, w.Body.String())
	}

	entries, err := srv.ledger.ListAudit(context.Background(), auditDeploy, 50)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 deploy audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Result != auditFailure {
		t.Errorf("result = %q, want %q", e.Result, auditFailure)
	}
	if e.Actor != "operator" {
		t.Errorf("actor = %q, want operator (default)", e.Actor)
	}
	if e.Target != "myapp" {
		t.Errorf("target = %q, want myapp", e.Target)
	}
	if !strings.Contains(e.Detail, "sha=abc123") {
		t.Errorf("detail %q missing sha", e.Detail)
	}
}

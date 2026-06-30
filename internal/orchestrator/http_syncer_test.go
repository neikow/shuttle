package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neikow/shuttle/internal/secrets"
	"github.com/neikow/shuttle/internal/webhook"
)

// newSyncerServer wires an HTTPServer to a GitSyncer backed by a real file:// IaC
// repo, so the plan/check/prune handlers exercise the actual git + diff + render
// paths (no Docker; git only).
func newSyncerServer(t *testing.T) *HTTPServer {
	t.Helper()
	src := makeSourceRepo(t)
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	// API_KEY must resolve at the service's secret folder for the check to pass.
	fake := secrets.NewFake(nil)
	fake.SetScope(secrets.Scope{Path: "/services/app"}, map[string]string{"API_KEY": "v"})
	syncer := NewGitSyncer("file://"+src, "main", t.TempDir(), store, reg, fake)
	srv := NewHTTPServer(testToken, store, reg)
	srv.EnableWebhook(webhook.NewHandler("whsecret", store), syncer)
	return srv
}

func TestHandlePlan(t *testing.T) {
	srv := newSyncerServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/plan"))
	if w.Code != http.StatusOK {
		t.Fatalf("plan: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var rep PlanReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	// Empty ledger -> the repo's one service is a "create".
	var foundCreate bool
	for _, e := range rep.Services {
		if e.Service == "app" && e.Action == PlanCreate {
			foundCreate = true
		}
	}
	if !foundCreate {
		t.Errorf("plan should show app as create, got %+v", rep.Services)
	}
}

func TestHandleCheck(t *testing.T) {
	srv := newSyncerServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/check"))
	if w.Code != http.StatusOK {
		t.Fatalf("check: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var rep CheckReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Errorf("check should pass (API_KEY resolves), got %+v", rep)
	}
}

func TestHandlePrune(t *testing.T) {
	srv := newSyncerServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/prune"))
	if w.Code != http.StatusOK {
		t.Fatalf("prune: want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePlan_unauthorized(t *testing.T) {
	srv := newSyncerServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plan", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

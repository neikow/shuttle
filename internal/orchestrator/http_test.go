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

const testToken = "secret-token"

func newHTTPTestServer(t *testing.T) *HTTPServer {
	t.Helper()
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	return NewHTTPServer(testToken, store, NewRegistry())
}

func openTestLedger(t *testing.T) (*ledger.Store, error) {
	t.Helper()
	s, err := ledger.Open(":memory:")
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { s.Close() })
	return s, nil
}

func authedRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func TestHealthz(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestListDeploys_unauthorized(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/deploys", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestListDeploys_empty(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/deploys"))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeploy_missingParams(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	// Missing sha and host.
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/deploy/myapp"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestDeploy_agentNotConnected(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/deploy/myapp?sha=abc&host=web1"))
	if w.Code != http.StatusBadGateway {
		t.Errorf("want 502 (agent not connected), got %d: %s", w.Code, w.Body.String())
	}
}

func TestRollback_noHistory(t *testing.T) {
	srv := newHTTPTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/rollback?service=app&host=web1"))
	if w.Code != http.StatusConflict {
		t.Errorf("want 409 (no rollback history), got %d: %s", w.Code, w.Body.String())
	}
}

// newRepoWebhookServer returns an HTTPServer with the repo-webhook endpoints
// enabled. The deployer stub records calls and returns the provided deploy IDs.
func newRepoWebhookServer(t *testing.T, deployIDs []string, deployErr error) *HTTPServer {
	t.Helper()
	srv := newHTTPTestServer(t)
	srv.repoWebhookDeployer = func(_ context.Context, _ []string) ([]string, error) {
		return deployIDs, deployErr
	}
	srv.mux.HandleFunc("POST /webhook/repo/{id}", srv.handleRepoWebhookTrigger)
	srv.mux.HandleFunc("POST /webhooks/repo", srv.bearerAuth(srv.handleCreateRepoWebhook))
	srv.mux.HandleFunc("GET /webhooks/repo", srv.bearerAuth(srv.handleListRepoWebhooks))
	srv.mux.HandleFunc("DELETE /webhooks/repo/{id}", srv.bearerAuth(srv.handleDeleteRepoWebhook))
	return srv
}

func TestRepoWebhookTrigger_unknownID(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/webhook/repo/doesnotexist", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestRepoWebhookTrigger_validID(t *testing.T) {
	srv := newRepoWebhookServer(t, []string{"deploy-1"}, nil)
	ctx := context.Background()

	id, err := srv.ledger.CreateRepoWebhook(ctx, "my-svc")
	if err != nil {
		t.Fatalf("CreateRepoWebhook: %v", err)
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/webhook/repo/"+id, strings.NewReader(`{}`)))
	if w.Code != http.StatusAccepted {
		t.Errorf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["deploy_ids"]; !ok {
		t.Errorf("response missing deploy_ids: %v", resp)
	}
}

func TestCreateRepoWebhook(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/repo", strings.NewReader(`{"service":"api"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.ID) != 64 {
		t.Errorf("want 64-char hex ID, got len=%d: %s", len(resp.ID), resp.ID)
	}
}

func TestCreateRepoWebhook_unauthorized(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/repo", strings.NewReader(`{"service":"api"}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestListRepoWebhooks_empty(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/webhooks/repo"))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []ledger.RepoWebhook
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("want empty list, got %d items", len(list))
	}
}

func TestListRepoWebhooks_unauthorized(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/webhooks/repo", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestDeleteRepoWebhook(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	ctx := context.Background()

	id, err := srv.ledger.CreateRepoWebhook(ctx, "to-delete")
	if err != nil {
		t.Fatalf("CreateRepoWebhook: %v", err)
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodDelete, "/webhooks/repo/"+id))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteRepoWebhook_notFound(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodDelete, "/webhooks/repo/nonexistent"))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestDeleteRepoWebhook_unauthorized(t *testing.T) {
	srv := newRepoWebhookServer(t, nil, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/webhooks/repo/someid", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

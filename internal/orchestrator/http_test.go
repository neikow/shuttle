package orchestrator

import (
	"net/http"
	"net/http/httptest"
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

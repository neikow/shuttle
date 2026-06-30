package orchestrator

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/infisical"
	"github.com/neikow/shuttle/internal/secrets"
)

func newInfisicalServer(t *testing.T) *HTTPServer {
	t.Helper()
	src := makeSourceRepo(t)
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	fake := secrets.NewFake(nil)
	fake.SetScope(secrets.Scope{Path: "/services/app"}, map[string]string{"API_KEY": "v"})
	syncer := NewGitSyncer("file://"+src, "main", t.TempDir(), store, NewRegistry(), fake)
	syncer.SetSecretsPaths("/shared", "/services/{service}")
	srv := NewHTTPServer(testToken, store, NewRegistry())
	// Empty secret -> unsigned payloads are accepted; short debounce so the
	// reconcile fires within the test.
	srv.EnableInfisicalWebhook(infisical.NewHandler(""), syncer, 20*time.Millisecond, "production")
	return srv
}

func post(t *testing.T, srv *HTTPServer, path, body string) int {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body))))
	return w.Code
}

func TestInfisicalWebhook(t *testing.T) {
	srv := newInfisicalServer(t)

	// Test ping -> 200.
	if code := post(t, srv, "/webhook/infisical", `{"event":"test"}`); code != http.StatusOK {
		t.Errorf("test event: want 200, got %d", code)
	}
	// Rotation-failed -> 200.
	if code := post(t, srv, "/webhook/infisical", `{"event":"secrets.rotation-failed"}`); code != http.StatusOK {
		t.Errorf("rotation-failed: want 200, got %d", code)
	}
	// Unknown event -> 200 (ignored).
	if code := post(t, srv, "/webhook/infisical", `{"event":"whatever"}`); code != http.StatusOK {
		t.Errorf("unknown event: want 200, got %d", code)
	}
	// Secrets modified -> 202, and the debounced reconcile runs.
	body := `{"event":"secrets.modified","project":{"environment":"production","secretPath":"/services/app"}}`
	if code := post(t, srv, "/webhook/infisical", body); code != http.StatusAccepted {
		t.Errorf("secrets.modified: want 202, got %d", code)
	}
	// Wait for the debounced reconcile to record a deploy for app.
	for range 100 {
		if recs, _ := srv.ledger.ListDeploys(context.Background(), "app", 1); len(recs) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("debounced infisical reconcile did not record a deploy")
}

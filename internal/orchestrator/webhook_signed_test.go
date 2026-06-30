package orchestrator

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestHandleWebhook_Signed(t *testing.T) {
	srv := newSyncerServer(t)
	body := []byte(`{"ref":"refs/heads/main","commit_sha":"x","repo":"r","services":[]}`)
	mac := hmac.New(sha256.New, []byte("whsecret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-Shuttle-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("signed webhook: want 202, got %d: %s", w.Code, w.Body.String())
	}

	// The async reconcile records a deploy for the repo's service; wait for it so
	// the goroutine finishes before the test's temp dirs are cleaned up.
	ctx := context.Background()
	for range 150 {
		if recs, _ := srv.ledger.ListDeploys(ctx, "app", 1); len(recs) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("webhook reconcile did not record a deploy")
}

func TestHandleWebhook_BadSignature(t *testing.T) {
	srv := newSyncerServer(t)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-Shuttle-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad signature: want 400, got %d", w.Code)
	}
}

func TestForceDeployAndHosts(t *testing.T) {
	g, store := syncedSyncer(t)
	ctx := context.Background()

	hosts, err := g.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web1" {
		t.Errorf("hosts = %+v, want [web1]", hosts)
	}

	if _, err := g.ForceDeploy(ctx, []string{"app"}); err != nil {
		t.Fatalf("ForceDeploy: %v", err)
	}
	if recs, _ := store.ListDeploys(ctx, "app", 1); len(recs) == 0 {
		t.Error("ForceDeploy should record a deploy for app")
	}
}

func TestHandleDeploy_NoAgent(t *testing.T) {
	srv := newSyncerServer(t)
	// Missing sha -> 400.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/deploy/app"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing sha: want 400, got %d", w.Code)
	}
	// With a sha but no connected agent -> DeployAtSHA fails -> 5xx.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/deploy/app?sha=deadbeef"))
	if w.Code < 400 {
		t.Errorf("deploy with no agent: want an error status, got %d", w.Code)
	}
}

func TestHandleRollback_NoTarget(t *testing.T) {
	srv := newSyncerServer(t)
	w := httptest.NewRecorder()
	// No prior deploy -> no rollback target -> error status.
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/rollback?service=app&steps=1"))
	if w.Code < 400 {
		t.Errorf("rollback with no target: want an error status, got %d", w.Code)
	}
}

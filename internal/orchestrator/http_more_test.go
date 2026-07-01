package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/ledger"
)

func TestHandleDeployLogs(t *testing.T) {
	srv := newHTTPTestServer(t)
	ctx := context.Background()
	_ = srv.ledger.RecordDeploy(ctx, ledger.DeployRecord{DeployID: "d1", Service: "web", Host: "h1", Status: ledger.StatusSuccess, StartedAt: time.Now()})
	if err := srv.ledger.RecordDeployLogs(ctx, "d1", []ledger.DeployLog{
		{At: time.Now(), Stream: "stdout", Text: "pulling"},
		{At: time.Now(), Stream: "stderr", Text: "warn"},
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/deploys/d1/logs"))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var logs []ledger.DeployLog
	if err := json.Unmarshal(w.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Errorf("logs = %d, want 2", len(logs))
	}
}

func TestRepoWebhooksCRUD(t *testing.T) {
	srv := newHTTPTestServer(t)
	srv.EnableRepoWebhooks(nil)

	// Create.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/repo", strings.NewReader(`{"service":"web"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("create returned no id")
	}

	// List.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/webhooks/repo"))
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}

	// Delete.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodDelete, "/webhooks/repo/"+created.ID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", w.Code)
	}
}

func TestBackupListAndLogs(t *testing.T) {
	srv := newHTTPTestServer(t)
	srv.EnableBackups(nil)
	ctx := context.Background()
	_ = srv.ledger.RecordBackup(ctx, ledger.BackupRecord{
		BackupID: "bk1", Service: "db", Host: "h1", Engine: "volume", Store: "local",
		Status: ledger.StatusSuccess, StartedAt: time.Now(),
	})
	_ = srv.ledger.RecordDeployLogs(ctx, "bk1", []ledger.DeployLog{{At: time.Now(), Stream: "stdout", Text: "backup done"}})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/backups?service=db"))
	if w.Code != http.StatusOK {
		t.Fatalf("list backups: want 200, got %d", w.Code)
	}
	var backups []ledger.BackupRecord
	if err := json.Unmarshal(w.Body.Bytes(), &backups); err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].BackupID != "bk1" {
		t.Errorf("backups = %+v, want bk1", backups)
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/backups/bk1/logs"))
	if w.Code != http.StatusOK {
		t.Fatalf("backup logs: want 200, got %d", w.Code)
	}
}

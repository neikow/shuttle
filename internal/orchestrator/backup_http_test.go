package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/ledger"
)

// backupTestServer registers the backup routes with stubbed dispatchers so the
// handlers can be exercised without a real GitSyncer.
func backupTestServer(t *testing.T) (*HTTPServer, *ledger.Store) {
	t.Helper()
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(testToken, store, NewRegistry())
	srv.EnableBackups(nil) // registers routes; dispatchers overridden below
	return srv, store
}

func TestHandleBackupTriggers(t *testing.T) {
	srv, _ := backupTestServer(t)
	var gotService string
	srv.backupDispatcher = func(_ context.Context, service string, trig ledger.TriggeredBy) (string, string, error) {
		gotService = service
		if trig != ledger.TriggeredByManual {
			t.Fatalf("want manual trigger, got %q", trig)
		}
		return "b-99", "host-1", nil
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/backup/db"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if gotService != "db" {
		t.Fatalf("dispatcher got service %q", gotService)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["backup_id"] != "b-99" || resp["host"] != "host-1" {
		t.Fatalf("unexpected body: %v", resp)
	}
}

func TestHandleBackupUnauthorized(t *testing.T) {
	srv, _ := backupTestServer(t)
	srv.backupDispatcher = func(context.Context, string, ledger.TriggeredBy) (string, string, error) {
		t.Fatal("dispatcher must not run for an unauthenticated request")
		return "", "", nil
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/backup/db", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandleListBackups(t *testing.T) {
	srv, store := backupTestServer(t)
	if err := store.RecordBackup(context.Background(), ledger.BackupRecord{
		BackupID: "b-1", Service: "db", Host: "h", Engine: "volume", Store: "local",
		Status: ledger.StatusSuccess, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodGet, "/backups?service=db"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got []ledger.BackupRecord
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].BackupID != "b-1" {
		t.Fatalf("unexpected backups: %+v", got)
	}
}

func TestHandleRestore(t *testing.T) {
	srv, _ := backupTestServer(t)
	var gotService, gotBackupID string
	srv.restoreDispatcher = func(_ context.Context, service, backupID string) (string, string, string, error) {
		gotService, gotBackupID = service, backupID
		return "op-1", "host-1", "snap-7", nil
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/restore?service=db&backup_id=b-3"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if gotService != "db" || gotBackupID != "b-3" {
		t.Fatalf("dispatcher got (%q,%q)", gotService, gotBackupID)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["operation_id"] != "op-1" || resp["snapshot_id"] != "snap-7" {
		t.Fatalf("unexpected body: %v", resp)
	}
}

func TestHandleRestoreRequiresService(t *testing.T) {
	srv, _ := backupTestServer(t)
	srv.restoreDispatcher = func(context.Context, string, string) (string, string, string, error) {
		t.Fatal("dispatcher must not run without a service")
		return "", "", "", nil
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, authedRequest(http.MethodPost, "/restore"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

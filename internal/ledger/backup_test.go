package ledger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	srcDir := t.TempDir()
	store, err := Open(filepath.Join(srcDir, DBFileName))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	rec := DeployRecord{
		DeployID:    "d1",
		Service:     "web",
		Host:        "h1",
		SHA:         "abc123",
		Status:      StatusPending,
		TriggeredBy: TriggeredByManual,
		StartedAt:   time.Now(),
	}
	if err := store.RecordDeploy(ctx, rec); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := store.MarkStatus(ctx, "d1", StatusSuccess); err != nil {
		t.Fatalf("mark: %v", err)
	}

	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if err := store.BackupTo(ctx, backup); err != nil {
		t.Fatalf("backup: %v", err)
	}
	_ = store.Close()

	// Backup to an existing path must fail.
	store2, _ := Open(filepath.Join(srcDir, DBFileName))
	if err := store2.BackupTo(ctx, backup); err == nil {
		t.Error("expected error backing up to an existing file")
	}
	_ = store2.Close()

	// Restore into a fresh data dir and confirm the deploy survived.
	restoreDir := t.TempDir()
	if err := RestoreInto(backup, restoreDir); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := Open(filepath.Join(restoreDir, DBFileName))
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = restored.Close() }()

	deploys, err := restored.ListDeploys(ctx, "web", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(deploys) != 1 {
		t.Fatalf("got %d deploys, want 1", len(deploys))
	}
	if deploys[0].DeployID != "d1" || deploys[0].Status != StatusSuccess {
		t.Errorf("restored deploy = %+v, want d1/success", deploys[0])
	}
}

func TestVerifyRejectsNonLedger(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "bogus.db")
	if err := RestoreInto(bogus, t.TempDir()); err == nil {
		t.Error("expected restore of a non-existent/invalid file to fail")
	}

	// A real file that isn't a ledger.
	plain := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(plain, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(plain); err == nil {
		t.Error("expected Verify to reject a non-ledger file")
	}
}

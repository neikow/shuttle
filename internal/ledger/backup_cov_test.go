package ledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDeploy(ctx, DeployRecord{
		DeployID: "d1", Service: "web", Host: "h", SHA: "abc",
		Status: StatusSuccess, TriggeredBy: TriggeredByManual, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.BackupTo(ctx, dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	_ = s.Close()

	if err := Verify(dest); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := Verify(filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Error("Verify of a missing file should error")
	}

	restoreDir := t.TempDir()
	if err := RestoreInto(dest, restoreDir); err != nil {
		t.Fatalf("RestoreInto: %v", err)
	}
	s2, err := Open(filepath.Join(restoreDir, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	recs, err := s2.ListDeploys(ctx, "web", 5)
	if err != nil || len(recs) != 1 || recs[0].SHA != "abc" {
		t.Errorf("restored ledger = %+v err=%v, want the original deploy", recs, err)
	}
}

func TestLatestSuccessfulBackup(t *testing.T) {
	ctx := context.Background()
	s := openMemory(t)
	rec := func(id string, st Status, ts time.Time) {
		if err := s.RecordBackup(ctx, BackupRecord{
			BackupID: id, Service: "db", Host: "h", Engine: "volume", Store: "local",
			Status: st, TriggeredBy: TriggeredByManual, StartedAt: ts,
		}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now()
	rec("b1", StatusSuccess, base)
	rec("b2", StatusFailed, base.Add(time.Minute))
	rec("b3", StatusSuccess, base.Add(2*time.Minute))

	got, ok, err := s.LatestSuccessfulBackup(ctx, "db")
	if err != nil || !ok {
		t.Fatalf("LatestSuccessfulBackup: ok=%v err=%v", ok, err)
	}
	if got.BackupID != "b3" {
		t.Errorf("latest = %q, want b3", got.BackupID)
	}
	if _, ok, _ := s.LatestSuccessfulBackup(ctx, "none"); ok {
		t.Error("unknown service should have no successful backup")
	}
}

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// resticStub answers a restic-store backup/restore flow: report the repo
// uninitialized (triggers init), emit a parseable backup summary, and resolve a
// compose volume; everything else (init, forget, tar, restore) succeeds.
const resticStub = `
case "$args" in
  *"cat config"*) exit 1 ;;
  *"config --format json"*) printf '{"name":"proj","volumes":{"data":{"name":"proj_data"}}}' ;;
  *backup*--json*) printf '{"message_type":"summary","snapshot_id":"snapX","total_bytes_processed":123}\n' ;;
  *"ps -q"*) echo cid ;;
  *) : ;;
esac
exit 0`

func TestBackup_VolumeRestic(t *testing.T) {
	d, _ := scriptDriver(t, resticStub)
	logs, done, err := d.Backup(context.Background(), BackupParams{
		BackupID: "bk1", Service: "app", Engine: "volume", Store: "restic",
		Target: "s3:bucket/x", WorkDir: backupWorkDir(t),
		Env:       map[string]string{"RESTIC_PASSWORD": "pw"},
		Retention: BackupRetention{KeepLast: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := runOutcome(t, logs, done)
	if out.Failed {
		t.Fatalf("restic backup failed: %s", out.Err)
	}
	if out.SnapshotID != "snapX" || out.SizeBytes != 123 {
		t.Errorf("outcome = %+v, want snapX/123 from the restic summary", out)
	}
}

func TestRestore_VolumeRestic(t *testing.T) {
	d, _ := scriptDriver(t, resticStub)
	logs, done, err := d.Restore(context.Background(), RestoreParams{
		OperationID: "op1", Service: "app", Engine: "volume", Store: "restic",
		Target: "s3:bucket/x", SnapshotID: "snapX", WorkDir: backupWorkDir(t),
		Env: map[string]string{"RESTIC_PASSWORD": "pw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := runOutcome(t, logs, done); out.Failed {
		t.Fatalf("restic restore failed: %s", out.Err)
	}
}

func TestRestore_PostgresLocal(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	target := t.TempDir()
	stage := filepath.Join(target, "db", "snap1")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "dump.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs, done, err := d.Restore(context.Background(), RestoreParams{
		OperationID: "op2", Service: "db", Engine: "postgres", Store: "local",
		Target: target, SnapshotID: "snap1", WorkDir: backupWorkDir(t), DBService: "pg", DBUser: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := runOutcome(t, logs, done); out.Failed {
		t.Fatalf("postgres restore failed: %s", out.Err)
	}
}

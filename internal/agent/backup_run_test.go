package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// backupStub answers the docker invocations a volume/postgres backup or restore
// makes: compose config (one volume), ps -q (a container id); everything else
// (helper-container run, exec, stop/start) just succeeds.
const backupStub = `
case "$args" in
  *"config --format json"*) printf '{"name":"proj","volumes":{"data":{"name":"proj_data"}}}' ;;
  *"ps -q"*) echo dbcid ;;
  *) : ;;
esac
exit 0`

func backupWorkDir(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return wd
}

func runOutcome(t *testing.T, logs <-chan LogLine, done <-chan BackupOutcome) BackupOutcome {
	t.Helper()
	drainText(logs)
	return <-done
}

func TestBackup_VolumeLocal(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	wd := backupWorkDir(t)
	target := t.TempDir()
	logs, done, err := d.Backup(context.Background(), BackupParams{
		BackupID: "bk1", Service: "app", Engine: "volume", Store: "local",
		Target: target, WorkDir: wd,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := runOutcome(t, logs, done)
	if out.Failed {
		t.Fatalf("backup failed: %s", out.Err)
	}
	if out.SnapshotID != "bk1" {
		t.Errorf("snapshot = %q, want bk1 (local store)", out.SnapshotID)
	}
}

func TestBackup_PostgresLocal(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	wd := backupWorkDir(t)
	logs, done, err := d.Backup(context.Background(), BackupParams{
		BackupID: "bk2", Service: "db", Engine: "postgres", Store: "local",
		Target: t.TempDir(), WorkDir: wd, DBService: "pg", DBUser: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := runOutcome(t, logs, done); out.Failed {
		t.Fatalf("postgres backup failed: %s", out.Err)
	}
}

func TestBackup_NoWorkspaceFails(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	logs, done, _ := d.Backup(context.Background(), BackupParams{
		BackupID: "x", Service: "app", Engine: "volume", Store: "local",
		Target: t.TempDir(), WorkDir: t.TempDir(), // empty, no compose file
	})
	if out := runOutcome(t, logs, done); !out.Failed {
		t.Error("backup with no compose workspace should fail")
	}
}

func TestBackup_UnknownEngineFails(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	logs, done, _ := d.Backup(context.Background(), BackupParams{
		BackupID: "x", Service: "app", Engine: "bogus", Store: "local",
		Target: t.TempDir(), WorkDir: backupWorkDir(t),
	})
	if out := runOutcome(t, logs, done); !out.Failed {
		t.Error("unknown engine should fail")
	}
}

func TestRestore_VolumeLocal(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	wd := backupWorkDir(t)
	target := t.TempDir()
	// A prior local backup: target/app/snap1/data.tar
	stage := filepath.Join(target, "app", "snap1")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "data.tar"), []byte("tardata"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs, done, err := d.Restore(context.Background(), RestoreParams{
		OperationID: "op1", Service: "app", Engine: "volume", Store: "local",
		Target: target, SnapshotID: "snap1", WorkDir: wd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := runOutcome(t, logs, done); out.Failed {
		t.Fatalf("restore failed: %s", out.Err)
	}
}

func TestRestore_MissingLocalSnapshotFails(t *testing.T) {
	d, _ := scriptDriver(t, backupStub)
	logs, done, _ := d.Restore(context.Background(), RestoreParams{
		Service: "app", Engine: "volume", Store: "local",
		Target: t.TempDir(), SnapshotID: "nope", WorkDir: backupWorkDir(t),
	})
	if out := runOutcome(t, logs, done); !out.Failed {
		t.Error("restore of a missing local snapshot should fail")
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0o700)
	if err := os.WriteFile(filepath.Join(sub, "b"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := dirSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("dirSize = %d, want 8", n)
	}
}

func TestServiceContainerID(t *testing.T) {
	d, _ := scriptDriver(t, `case "$args" in *"ps -q"*) printf 'cid1\ncid2\n' ;; esac
exit 0`)
	wd := backupWorkDir(t)
	cid, err := d.serviceContainerID(context.Background(),
		filepath.Join(wd, "docker-compose.yml"), filepath.Join(wd, ".env"), "db")
	if err != nil {
		t.Fatal(err)
	}
	if cid != "cid1" {
		t.Errorf("serviceContainerID = %q, want cid1 (first of several)", cid)
	}

	// No container -> error.
	d2, _ := scriptDriver(t, "exit 0")
	if _, err := d2.serviceContainerID(context.Background(),
		filepath.Join(wd, "docker-compose.yml"), filepath.Join(wd, ".env"), "db"); err == nil {
		t.Error("empty ps output should error")
	}
}

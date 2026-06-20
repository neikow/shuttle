package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/ledger"
)

func TestResolveBackupDefaults(t *testing.T) {
	g := &GitSyncer{backupCfg: config.BackupConfig{DefaultStore: "restic", DefaultTarget: "s3:bucket"}}

	// Service omits store/target → inherits bootstrap defaults.
	got := g.resolveBackup(config.Service{Backup: &config.ServiceBackup{Engine: "volume"}})
	if got.Store != "restic" || got.Target != "s3:bucket" {
		t.Fatalf("defaults not inherited: %+v", got)
	}

	// Explicit values win over defaults.
	got = g.resolveBackup(config.Service{Backup: &config.ServiceBackup{Engine: "volume", Store: "local", Target: "/srv/b"}})
	if got.Store != "local" || got.Target != "/srv/b" {
		t.Fatalf("explicit values overridden: %+v", got)
	}

	// No defaults configured and no store → falls back to local.
	bare := &GitSyncer{}
	got = bare.resolveBackup(config.Service{Backup: &config.ServiceBackup{Engine: "volume"}})
	if got.Store != config.BackupStoreLocal {
		t.Fatalf("want local fallback, got %q", got.Store)
	}

	// No backup policy → nil.
	if bare.resolveBackup(config.Service{}) != nil {
		t.Fatal("service without backup should resolve to nil")
	}
}

func TestBackupEventType(t *testing.T) {
	cases := []struct {
		op   string
		ls   ledger.Status
		want EventType
	}{
		{"backup", ledger.StatusSuccess, EventBackupSucceeded},
		{"backup", ledger.StatusFailed, EventBackupFailed},
		{"restore", ledger.StatusSuccess, EventRestoreSucceeded},
		{"restore", ledger.StatusFailed, EventRestoreFailed},
	}
	for _, c := range cases {
		if got := backupEventType(c.op, c.ls); got != c.want {
			t.Fatalf("backupEventType(%q,%v) = %v, want %v", c.op, c.ls, got, c.want)
		}
	}
}

func TestRetentionToProto(t *testing.T) {
	p := retentionToProto(config.BackupRetention{KeepLast: 3, KeepWeekly: 4})
	if p.KeepLast != 3 || p.KeepWeekly != 4 || p.KeepDaily != 0 {
		t.Fatalf("retention mapping wrong: %+v", p)
	}
}

func TestHandleBackupResultUpdatesLedgerAndEmits(t *testing.T) {
	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.RecordBackup(ctx, ledger.BackupRecord{
		BackupID: "b-1", Service: "db", Host: "h1", Engine: "volume", Store: "local",
		Status: ledger.StatusPending, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	bus := NewEventBus()
	sub, _ := bus.Subscribe()
	defer sub.Close()

	srv := NewAgentServiceServer(NewRegistry(), store)
	srv.SetEventBus(bus)
	srv.handleBackupResult(ctx, "h1", &shuttlev1.BackupResult{
		OperationId: "b-1", Operation: "backup", Service: "db",
		Status: shuttlev1.BackupStatus_BACKUP_STATUS_SUCCESS, SnapshotId: "snap-1", SizeBytes: 99,
		Logs: []*shuttlev1.LogLine{{TsUnixMs: time.Now().UnixMilli(), Stream: "stdout", Text: "done"}},
	})

	got, ok, err := store.BackupByID(ctx, "b-1")
	if err != nil || !ok {
		t.Fatalf("BackupByID: ok=%v err=%v", ok, err)
	}
	if got.Status != ledger.StatusSuccess || got.SnapshotID != "snap-1" || got.SizeBytes != 99 {
		t.Fatalf("ledger not finalized: %+v", got)
	}
	// Logs persisted under the operation id.
	logs, _ := store.DeployLogs(ctx, "b-1")
	if len(logs) != 1 || logs[0].Text != "done" {
		t.Fatalf("backup logs not persisted: %+v", logs)
	}

	select {
	case ev := <-sub.C:
		if ev.Type != EventBackupSucceeded || ev.Service != "db" || ev.Detail["snapshot"] != "snap-1" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event published")
	}
}

func TestHandleBackupResultRestoreNoLedgerRow(t *testing.T) {
	store, err := ledger.Open(filepath.Join(t.TempDir(), "led.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	bus := NewEventBus()
	sub, _ := bus.Subscribe()
	defer sub.Close()

	srv := NewAgentServiceServer(NewRegistry(), store)
	srv.SetEventBus(bus)
	// A restore has no service_backups row; the handler must not error and must
	// still emit a restore event.
	srv.handleBackupResult(ctx, "h1", &shuttlev1.BackupResult{
		OperationId: "op-9", Operation: "restore", Service: "db",
		Status: shuttlev1.BackupStatus_BACKUP_STATUS_FAILED, Error: "boom",
	})
	select {
	case ev := <-sub.C:
		if ev.Type != EventRestoreFailed || ev.Message != "boom" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no restore event published")
	}
}

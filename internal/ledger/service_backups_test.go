package ledger

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndMarkBackup(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	rec := BackupRecord{
		BackupID:    "b-1",
		Service:     "db",
		Host:        "host-1",
		Engine:      "postgres",
		Store:       "restic",
		Target:      "s3:bucket/db",
		Status:      StatusPending,
		TriggeredBy: TriggeredBySchedule,
		StartedAt:   time.Now(),
	}
	if err := s.RecordBackup(ctx, rec); err != nil {
		t.Fatalf("RecordBackup: %v", err)
	}

	got, ok, err := s.BackupByID(ctx, "b-1")
	if err != nil || !ok {
		t.Fatalf("BackupByID: ok=%v err=%v", ok, err)
	}
	if got.Status != StatusPending || got.FinishedAt != nil {
		t.Fatalf("pending row wrong: %+v", got)
	}

	if err := s.MarkBackupResult(ctx, "b-1", StatusSuccess, "snap-abc", 4096, ""); err != nil {
		t.Fatalf("MarkBackupResult: %v", err)
	}
	got, _, _ = s.BackupByID(ctx, "b-1")
	if got.Status != StatusSuccess || got.SnapshotID != "snap-abc" || got.SizeBytes != 4096 {
		t.Fatalf("success row wrong: %+v", got)
	}
	if got.FinishedAt == nil {
		t.Fatal("finished_at should be set on success")
	}
}

func TestBackupByIDMissing(t *testing.T) {
	s := openMemory(t)
	_, ok, err := s.BackupByID(context.Background(), "nope")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("missing id should report ok=false")
	}
}

func TestListBackupsAndLatest(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	base := time.Now()

	mk := func(id string, off time.Duration, status Status) {
		if err := s.RecordBackup(ctx, BackupRecord{
			BackupID: id, Service: "db", Host: "h", Engine: "volume", Store: "local",
			Status: StatusPending, StartedAt: base.Add(off),
		}); err != nil {
			t.Fatalf("RecordBackup %s: %v", id, err)
		}
		if status != StatusPending {
			if err := s.MarkBackupResult(ctx, id, status, "snap-"+id, 1, ""); err != nil {
				t.Fatalf("mark %s: %v", id, err)
			}
		}
	}
	mk("old", -2*time.Hour, StatusSuccess)
	mk("mid", -time.Hour, StatusFailed)
	mk("new", 0, StatusSuccess)
	// Another service must not bleed into per-service queries.
	if err := s.RecordBackup(ctx, BackupRecord{
		BackupID: "other", Service: "web", Host: "h", Engine: "volume", Store: "local",
		Status: StatusPending, StartedAt: base,
	}); err != nil {
		t.Fatalf("RecordBackup other: %v", err)
	}

	list, err := s.ListBackups(ctx, "db", 10)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 db backups, got %d", len(list))
	}
	if list[0].BackupID != "new" {
		t.Fatalf("newest first expected, got %q", list[0].BackupID)
	}

	latest, ok, err := s.LatestBackupStart(ctx, "db")
	if err != nil || !ok {
		t.Fatalf("LatestBackupStart: ok=%v err=%v", ok, err)
	}
	if latest.UnixMilli() != base.UnixMilli() {
		t.Fatalf("latest start = %v, want %v", latest, base)
	}

	// Most recent *successful* backup is "new" (failed "mid" is skipped).
	succ, ok, err := s.LatestSuccessfulBackup(ctx, "db")
	if err != nil || !ok {
		t.Fatalf("LatestSuccessfulBackup: ok=%v err=%v", ok, err)
	}
	if succ.BackupID != "new" || succ.SnapshotID != "snap-new" {
		t.Fatalf("latest successful wrong: %+v", succ)
	}
}

func TestLatestBackupStartNone(t *testing.T) {
	s := openMemory(t)
	_, ok, err := s.LatestBackupStart(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("no backups should report ok=false")
	}
}

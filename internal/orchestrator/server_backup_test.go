package orchestrator

import (
	"context"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/ledger"
)

func TestBackupResult_FinalizesLedger(t *testing.T) {
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	ctxBg := context.Background()
	if err := store.RecordBackup(ctxBg, ledger.BackupRecord{
		BackupID: "bk1", Service: "db", Host: "web1", Engine: "volume", Store: "local",
		Status: ledger.StatusPending, TriggeredBy: ledger.TriggeredByManual, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, _, client := newTestServerWithLedger(t, store)
	ctx, cancel := context.WithTimeout(ctxBg, 5*time.Second)
	defer cancel()
	stream, err := client.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&shuttlev1.AgentEvent{Payload: &shuttlev1.AgentEvent_Register{Register: &shuttlev1.RegisterRequest{Host: "web1"}}})
	_ = stream.Send(&shuttlev1.AgentEvent{Payload: &shuttlev1.AgentEvent_BackupResult{
		BackupResult: &shuttlev1.BackupResult{
			OperationId: "bk1", Operation: "backup", Status: shuttlev1.BackupStatus_BACKUP_STATUS_SUCCESS,
			SnapshotId: "snap-1", SizeBytes: 99, Service: "db",
		},
	}})
	_ = stream.CloseSend()

	for range 100 {
		rec, ok, _ := store.BackupByID(ctxBg, "bk1")
		if ok && rec.Status == ledger.StatusSuccess {
			if rec.SnapshotID != "snap-1" || rec.SizeBytes != 99 {
				t.Errorf("finalized backup = %+v, want snap-1/99", rec)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("backup row was not finalized to success")
}

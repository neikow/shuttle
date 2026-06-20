package ledger

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndGetDeployLogs(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	base := time.Now()
	logs := []DeployLog{
		{At: base, Stream: "stdout", Text: "Pulling image"},
		{At: base.Add(time.Second), Stream: "stdout", Text: "Creating container"},
		{At: base.Add(2 * time.Second), Stream: "stderr", Text: "warning: deprecated flag"},
	}
	if err := s.RecordDeployLogs(ctx, "deploy-1", logs); err != nil {
		t.Fatalf("RecordDeployLogs: %v", err)
	}

	got, err := s.DeployLogs(ctx, "deploy-1")
	if err != nil {
		t.Fatalf("DeployLogs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3", len(got))
	}
	// Order is preserved by seq.
	if got[0].Text != "Pulling image" || got[2].Text != "warning: deprecated flag" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[2].Stream != "stderr" {
		t.Fatalf("stream not preserved: %q", got[2].Stream)
	}
	if !got[0].At.Equal(base.Truncate(time.Millisecond)) {
		t.Fatalf("timestamp not preserved: got %v want %v", got[0].At, base.Truncate(time.Millisecond))
	}
}

func TestDeployLogsEmptyCases(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	// Unknown deploy id → empty, non-nil slice (encodes as [] not null).
	got, err := s.DeployLogs(ctx, "nope")
	if err != nil {
		t.Fatalf("DeployLogs: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}

	// Recording no lines (or an empty deploy id) is a no-op, not an error.
	if err := s.RecordDeployLogs(ctx, "deploy-x", nil); err != nil {
		t.Fatalf("RecordDeployLogs(nil): %v", err)
	}
	if err := s.RecordDeployLogs(ctx, "", []DeployLog{{Text: "x"}}); err != nil {
		t.Fatalf("RecordDeployLogs(empty id): %v", err)
	}

	// A blank stream defaults to stdout.
	if err := s.RecordDeployLogs(ctx, "deploy-2", []DeployLog{{Text: "line"}}); err != nil {
		t.Fatalf("RecordDeployLogs: %v", err)
	}
	got, err = s.DeployLogs(ctx, "deploy-2")
	if err != nil {
		t.Fatalf("DeployLogs: %v", err)
	}
	if len(got) != 1 || got[0].Stream != "stdout" {
		t.Fatalf("default stream not applied: %+v", got)
	}
}

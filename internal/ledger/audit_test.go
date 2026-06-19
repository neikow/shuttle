package ledger

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndListAudit(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	entries := []AuditEntry{
		{Actor: "alice", Action: "deploy", Target: "web", SourceIP: "10.0.0.1", Result: "success", Detail: "sha=abc"},
		{Actor: "bob", Action: "rollback", Target: "web", SourceIP: "10.0.0.2", Result: "success"},
		{Actor: "alice", Action: "deploy", Target: "api", SourceIP: "10.0.0.1", Result: "failure", Detail: "boom"},
	}
	for i, e := range entries {
		// Stagger timestamps so ordering is deterministic (newest first).
		e.At = time.Now().Add(time.Duration(i) * time.Millisecond)
		id, err := s.RecordAudit(ctx, e)
		if err != nil {
			t.Fatalf("RecordAudit: %v", err)
		}
		if id == "" {
			t.Fatal("RecordAudit returned empty id")
		}
	}

	all, err := s.ListAudit(ctx, "", 50)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d entries, want 3", len(all))
	}
	// Newest first: the last-inserted (api/failure) entry leads.
	if all[0].Target != "api" || all[0].Result != "failure" || all[0].Detail != "boom" {
		t.Fatalf("unexpected newest entry: %+v", all[0])
	}
	if all[0].Actor != "alice" || all[0].Action != "deploy" || all[0].SourceIP != "10.0.0.1" {
		t.Fatalf("unexpected fields on newest entry: %+v", all[0])
	}

	deploys, err := s.ListAudit(ctx, "deploy", 50)
	if err != nil {
		t.Fatalf("ListAudit(deploy): %v", err)
	}
	if len(deploys) != 2 {
		t.Fatalf("got %d deploy entries, want 2", len(deploys))
	}
	for _, e := range deploys {
		if e.Action != "deploy" {
			t.Fatalf("action filter leaked: %+v", e)
		}
	}

	limited, err := s.ListAudit(ctx, "", 1)
	if err != nil {
		t.Fatalf("ListAudit(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit not honored: got %d, want 1", len(limited))
	}
}

func TestListAudit_empty(t *testing.T) {
	s := openMemory(t)
	got, err := s.ListAudit(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no entries, got %d", len(got))
	}
}

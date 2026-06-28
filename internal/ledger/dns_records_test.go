package ledger

import (
	"context"
	"testing"
)

func TestDNSRecords_UpsertListDelete(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()

	rec := DNSRecord{
		FQDN: "app.example.com", Type: "A", Value: "203.0.113.20",
		Provider: "ovh", Zone: "example.com", Service: "app", Host: "web1",
	}
	if err := s.UpsertDNSRecord(ctx, rec); err != nil {
		t.Fatalf("UpsertDNSRecord: %v", err)
	}

	got, err := s.ListDNSRecords(ctx)
	if err != nil {
		t.Fatalf("ListDNSRecords: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
	if got[0].Value != "203.0.113.20" || got[0].Provider != "ovh" || got[0].UpdatedAt.IsZero() {
		t.Errorf("unexpected record: %+v", got[0])
	}

	// Upsert with the same key updates value in place (no duplicate).
	rec.Value = "203.0.113.99"
	if err := s.UpsertDNSRecord(ctx, rec); err != nil {
		t.Fatalf("UpsertDNSRecord update: %v", err)
	}
	got, _ = s.ListDNSRecords(ctx)
	if len(got) != 1 || got[0].Value != "203.0.113.99" {
		t.Fatalf("after update: %+v", got)
	}

	// A different type at the same name is a distinct row.
	if err := s.UpsertDNSRecord(ctx, DNSRecord{FQDN: "app.example.com", Type: "AAAA", Value: "2001:db8::1", Provider: "ovh", Zone: "example.com", Service: "app"}); err != nil {
		t.Fatalf("upsert AAAA: %v", err)
	}
	if got, _ = s.ListDNSRecords(ctx); len(got) != 2 {
		t.Fatalf("records = %d, want 2 (A + AAAA)", len(got))
	}

	if err := s.DeleteDNSRecord(ctx, "app.example.com", "A"); err != nil {
		t.Fatalf("DeleteDNSRecord: %v", err)
	}
	got, _ = s.ListDNSRecords(ctx)
	if len(got) != 1 || got[0].Type != "AAAA" {
		t.Fatalf("after delete: %+v", got)
	}
}

package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/dns"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

// fakeDNSManager records calls and returns a fixed owned set, for testing the
// reconciler without a real provider.
type fakeDNSManager struct {
	owned   []dns.Record
	ensured []dns.Record
	removed []dns.Record
}

func (f *fakeDNSManager) Ensure(_ context.Context, _ string, r dns.Record) error {
	f.ensured = append(f.ensured, r)
	return nil
}
func (f *fakeDNSManager) Remove(_ context.Context, _ string, r dns.Record) error {
	f.removed = append(f.removed, r)
	return nil
}
func (f *fakeDNSManager) Owned(_ context.Context, _ string) ([]dns.Record, error) {
	return f.owned, nil
}

const dnsReconcilerDNSYML = `providers:
  - name: ovh
    type: ovh
    endpoint: ovh-eu
    credentials:
      application_key:    { infisical_key: OVH_APP_KEY }
      application_secret: { infisical_key: OVH_APP_SECRET }
      consumer_key:       { infisical_key: OVH_CONSUMER_KEY }
  - name: home
    type: manual
zones:
  - domain: example.com
    provider: ovh
  - domain: home.example.com
    provider: home
    address: tailscale
`

func writeDNSReconcilerRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hosts.yaml", "hosts:\n  - name: web1\n    addresses:\n      public: 203.0.113.20\n      tailscale: 100.64.0.5\n")
	write("dns.yml", dnsReconcilerDNSYML)
	// public domain (ovh) + private domain (manual)
	write("services/app/app.yaml", "name: app\nhost: web1\ndomains: [app.example.com, app.home.example.com]\n")
	write("services/app/docker-compose.yml", "services: {}\n")
	return dir
}

func newDNSReconcilerTest(t *testing.T, fake *fakeDNSManager) (*DNSReconciler, *ledger.Store) {
	t.Helper()
	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sec := secrets.NewFake(map[string]string{
		"OVH_APP_KEY": "ak", "OVH_APP_SECRET": "as", "OVH_CONSUMER_KEY": "ck",
	})
	syncer := NewGitSyncer("", "main", writeDNSReconcilerRepo(t), store, NewRegistry(), sec)

	r := NewDNSReconciler(syncer, store, time.Minute)
	r.newManager = func(_, _ string, _ map[string]string) (dns.Manager, error) { return fake, nil }
	return r, store
}

func TestDNSReconciler_CreatesAndSkipsManual(t *testing.T) {
	fake := &fakeDNSManager{}
	r, store := newDNSReconcilerTest(t, fake)
	r.tick(context.Background())

	// The ovh zone record is created; the manual-zone record is not (skipped).
	if len(fake.ensured) != 1 {
		t.Fatalf("ensured = %+v, want exactly the ovh record", fake.ensured)
	}
	got := fake.ensured[0]
	if got.Name != "app.example.com" || got.Type != "A" || got.Value != "203.0.113.20" {
		t.Errorf("ensured = %+v, want app.example.com A 203.0.113.20", got)
	}
	for _, e := range fake.ensured {
		if e.Name == "app.home.example.com" {
			t.Error("manual-zone record should not be applied")
		}
	}

	// Ledger mirrors only the managed (ovh) record.
	recs, _ := store.ListDNSRecords(context.Background())
	if len(recs) != 1 || recs[0].FQDN != "app.example.com" || recs[0].Provider != "ovh" {
		t.Fatalf("ledger = %+v, want one ovh record", recs)
	}
}

func TestDNSReconciler_HealsDrift(t *testing.T) {
	// Provider has our record but with a stale value → reconciler heals it.
	fake := &fakeDNSManager{owned: []dns.Record{{Name: "app.example.com", Type: "A", Value: "9.9.9.9"}}}
	r, _ := newDNSReconcilerTest(t, fake)
	r.tick(context.Background())

	if len(fake.ensured) != 1 || fake.ensured[0].Value != "203.0.113.20" {
		t.Fatalf("ensured = %+v, want heal to 203.0.113.20", fake.ensured)
	}
	if len(fake.removed) != 0 {
		t.Errorf("removed = %+v, want none (value drift heals, not deletes)", fake.removed)
	}
}

func TestCheckDNSRecords(t *testing.T) {
	repo, err := config.Load(writeDNSReconcilerRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	checks := (&GitSyncer{}).CheckDNSRecords(repo)

	byFQDN := map[string]DNSRecordCheck{}
	for _, c := range checks {
		byFQDN[c.FQDN] = c
	}
	pub, ok := byFQDN["app.example.com"]
	if !ok || pub.Manual || pub.Provider != "ovh" || pub.Value != "203.0.113.20" {
		t.Errorf("public record = %+v, want ovh/non-manual/203.0.113.20", pub)
	}
	priv, ok := byFQDN["app.home.example.com"]
	if !ok || !priv.Manual || priv.Value != "100.64.0.5" {
		t.Errorf("private record = %+v, want manual/100.64.0.5", priv)
	}
}

func TestCheckDNSRecords_MissingHostAddress(t *testing.T) {
	dir := writeDNSReconcilerRepo(t)
	// Drop the tailscale address the home zone needs.
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"),
		[]byte("hosts:\n  - name: web1\n    addresses:\n      public: 203.0.113.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var problem bool
	for _, c := range (&GitSyncer{}).CheckDNSRecords(repo) {
		if c.Err != "" {
			problem = true
		}
	}
	if !problem {
		t.Error("expected a problem for the host missing its tailscale address")
	}
}

func TestDNSReconciler_RemovesUndesired(t *testing.T) {
	// Provider owns a record we no longer want → reconciler removes it (and keeps
	// creating the desired one).
	fake := &fakeDNSManager{owned: []dns.Record{{Name: "old.example.com", Type: "A", Value: "1.1.1.1"}}}
	r, store := newDNSReconcilerTest(t, fake)
	// Pre-seed the ledger with the stale record so we can confirm it's pruned.
	_ = store.UpsertDNSRecord(context.Background(), ledger.DNSRecord{FQDN: "old.example.com", Type: "A", Value: "1.1.1.1", Provider: "ovh", Zone: "example.com", Service: "gone"})
	r.tick(context.Background())

	if len(fake.removed) != 1 || fake.removed[0].Name != "old.example.com" {
		t.Fatalf("removed = %+v, want old.example.com", fake.removed)
	}
	recs, _ := store.ListDNSRecords(context.Background())
	for _, rec := range recs {
		if rec.FQDN == "old.example.com" {
			t.Error("stale record should be pruned from the ledger")
		}
	}
}

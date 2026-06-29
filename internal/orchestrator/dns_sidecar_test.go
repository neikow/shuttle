package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/dns"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/secrets"
)

func TestRenderZoneFile(t *testing.T) {
	recs := []dns.Record{
		{Name: "b.home.example.com", Type: "A", Value: "100.64.0.6"},
		{Name: "app.home.example.com", Type: "A", Value: "100.64.0.5"},
	}
	zf := renderZoneFile("home.example.com", recs, "100.64.0.1", 12345)

	for _, want := range []string{
		"$ORIGIN home.example.com.",
		"@ IN SOA ns.home.example.com. hostmaster.home.example.com.",
		"12345 ; serial",
		"@ IN NS ns.home.example.com.",
		"ns IN A 100.64.0.1",
		"app.home.example.com. IN A 100.64.0.5",
		"b.home.example.com. IN A 100.64.0.6",
	} {
		if !strings.Contains(zf, want) {
			t.Errorf("zone file missing %q\n---\n%s", want, zf)
		}
	}
	// Deterministic ordering: app sorts before b.
	if strings.Index(zf, "app.home.example.com.") > strings.Index(zf, "b.home.example.com.") {
		t.Error("records not sorted by name")
	}

	// No NS glue when nsIP is absent/invalid.
	if got := renderZoneFile("home.example.com", nil, "", 1); strings.Contains(got, "ns IN A") {
		t.Errorf("expected no ns glue with empty nsIP:\n%s", got)
	}
	// IPv6 glue uses AAAA.
	if got := renderZoneFile("home.example.com", nil, "fd7a::1", 1); !strings.Contains(got, "ns IN AAAA fd7a::1") {
		t.Errorf("expected AAAA ns glue:\n%s", got)
	}

	// CNAME records get an FQDN (trailing-dot) target.
	cn := renderZoneFile("home.example.com", []dns.Record{{Name: "app.home.example.com", Type: "CNAME", Value: "lb.example.net"}}, "", 1)
	if !strings.Contains(cn, "app.home.example.com. IN CNAME lb.example.net.") {
		t.Errorf("expected CNAME line with FQDN target:\n%s", cn)
	}
}

func TestRecordType(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5": "A",
		"2001:db8::1": "AAAA",
		"lb.example.net": "CNAME",
	}
	for in, want := range cases {
		if got, prob := recordType(in); got != want || prob != "" {
			t.Errorf("recordType(%q) = %q,%q want %q", in, got, prob, want)
		}
	}
	if _, prob := recordType("not a host"); prob == "" {
		t.Error("expected a problem for a non-IP non-hostname value")
	}
}

func TestDNSReconciler_SidecarZoneNotManagerReconciled(t *testing.T) {
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
	write("hosts.yaml", "hosts:\n  - name: web1\n    addresses:\n      tailscale: 100.64.0.5\n")
	write("dns.yml", "providers:\n  - name: home\n    type: sidecar\n    host: web1\n"+
		"zones:\n  - domain: home.example.com\n    provider: home\n    address: tailscale\n")
	write("services/app/app.yaml", "name: app\nhost: web1\ndomains: [app.home.example.com]\n")
	write("services/app/docker-compose.yml", "services: {}\n")

	store, err := ledger.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	syncer := NewGitSyncer("", "main", dir, store, NewRegistry(), secrets.NewFake(nil))

	fake := &fakeDNSManager{}
	r := NewDNSReconciler(syncer, store, time.Minute)
	r.newManager = func(_, _ string, _ map[string]string) (dns.Manager, error) { return fake, nil }

	// Tick must not error and must not route the sidecar zone through the Manager
	// (the agent push path handles it; the host isn't connected so dispatch is a
	// no-op skip).
	r.tick(context.Background())

	if len(fake.ensured) != 0 || len(fake.removed) != 0 {
		t.Errorf("sidecar zone should not go through the record Manager: ensured=%v removed=%v", fake.ensured, fake.removed)
	}
	if recs, _ := store.ListDNSRecords(context.Background()); len(recs) != 0 {
		t.Errorf("sidecar records should not be ledgered (zone file is the state): %+v", recs)
	}
}

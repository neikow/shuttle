package orchestrator

import (
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/dns"
)

// sidecarZones accumulates the zone files destined for one host's CoreDNS sidecar.
type sidecarZones struct {
	port  int
	files []*shuttlev1.DNSZoneFile
}

// renderZoneFile renders an RFC1035 zone file for origin serving records. nsIP,
// when a valid IP, is published as the in-zone glue for the SOA/NS authority
// (the sidecar host's own address); serial bumps the SOA so CoreDNS's file
// plugin reloads on change. Records are sorted for deterministic output.
func renderZoneFile(origin string, records []dns.Record, nsIP string, serial int64) string {
	var b strings.Builder
	fqdn := func(s string) string {
		if strings.HasSuffix(s, ".") {
			return s
		}
		return s + "."
	}
	o := fqdn(origin)
	fmt.Fprintf(&b, "$ORIGIN %s\n$TTL 300\n", o)
	fmt.Fprintf(&b, "@ IN SOA ns.%s hostmaster.%s (\n", o, o)
	fmt.Fprintf(&b, "\t%d ; serial\n\t7200 ; refresh\n\t3600 ; retry\n\t1209600 ; expire\n\t300 ) ; minimum\n", serial)
	fmt.Fprintf(&b, "@ IN NS ns.%s\n", o)
	if ip, err := netip.ParseAddr(nsIP); err == nil {
		rr := "A"
		if ip.Is6() {
			rr = "AAAA"
		}
		fmt.Fprintf(&b, "ns IN %s %s\n", rr, ip.String())
	}

	sorted := append([]dns.Record(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Type < sorted[j].Type
	})
	for _, r := range sorted {
		val := r.Value
		if r.Type == "CNAME" {
			val = fqdn(val) // CNAME targets are FQDNs in a zone file
		}
		fmt.Fprintf(&b, "%s IN %s %s\n", fqdn(r.Name), r.Type, val)
	}
	return b.String()
}

// dispatchSidecar pushes each host's accumulated zone files to its agent's
// CoreDNS sidecar. Best-effort: a disconnected host is skipped and healed on the
// next tick (declarative full-zone push, like the Caddy config path).
func (d *DNSReconciler) dispatchSidecar(byHost map[string]*sidecarZones) {
	for host, sz := range byHost {
		cmd := &shuttlev1.OrchestratorCommand{
			Payload: &shuttlev1.OrchestratorCommand_DnsConfig{
				DnsConfig: &shuttlev1.DNSConfigRequest{Zones: sz.files, Port: int32(sz.port)},
			},
		}
		if err := d.syncer.registry.Send(host, cmd); err != nil {
			slog.Debug("skip dns sidecar push (host not connected)", "host", host, "err", err)
			continue
		}
		slog.Info("dns sidecar config pushed", "host", host, "zones", len(sz.files))
	}
}

// recordsOf extracts the dns.Records from a slice of desiredRecords.
func recordsOf(drs []desiredRecord) []dns.Record {
	out := make([]dns.Record, 0, len(drs))
	for _, dr := range drs {
		out = append(out, dr.rec)
	}
	return out
}

// hostMap indexes the repo's hosts by name.
func hostMap(repo *config.Repo) map[string]config.Host {
	m := make(map[string]config.Host, len(repo.Hosts))
	for _, h := range repo.Hosts {
		m[h.Name] = h
	}
	return m
}

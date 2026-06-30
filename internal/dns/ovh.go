package dns

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/libdns/libdns"
	"github.com/libdns/ovh"
)

// ovhManager manages records in an OVH DNS zone via the libdns OVH provider.
type ovhManager struct{ p *ovh.Provider }

func newOVHManager(endpoint string, creds map[string]string) (*ovhManager, error) {
	p := &ovh.Provider{
		Endpoint:          endpoint,
		ApplicationKey:    creds["application_key"],
		ApplicationSecret: creds["application_secret"],
		ConsumerKey:       creds["consumer_key"],
	}
	if p.Endpoint == "" {
		return nil, fmt.Errorf("ovh: endpoint is required")
	}
	if p.ApplicationKey == "" || p.ApplicationSecret == "" || p.ConsumerKey == "" {
		return nil, fmt.Errorf("ovh: missing credentials (need application_key, application_secret, consumer_key)")
	}
	return &ovhManager{p: p}, nil
}

func (m *ovhManager) Ensure(ctx context.Context, zone string, r Record) error {
	z := zoneFQDN(zone)
	rec, err := recordFor(z, r)
	if err != nil {
		return err
	}
	if _, err := m.p.SetRecords(ctx, z, []libdns.Record{rec}); err != nil {
		return fmt.Errorf("ovh set %s: %w", r.Name, err)
	}
	txt := ownerTXTRecord(z, r.Name)
	if _, err := m.p.SetRecords(ctx, z, []libdns.Record{txt}); err != nil {
		return fmt.Errorf("ovh set owner txt for %s: %w", r.Name, err)
	}
	return nil
}

func (m *ovhManager) Remove(ctx context.Context, zone string, r Record) error {
	z := zoneFQDN(zone)
	rec, err := recordFor(z, r)
	if err != nil {
		return err
	}
	del := []libdns.Record{rec, ownerTXTRecord(z, r.Name)}
	if _, err := m.p.DeleteRecords(ctx, z, del); err != nil {
		return fmt.Errorf("ovh delete %s: %w", r.Name, err)
	}
	return nil
}

func (m *ovhManager) Owned(ctx context.Context, zone string) ([]Record, error) {
	z := zoneFQDN(zone)
	recs, err := m.p.GetRecords(ctx, z)
	if err != nil {
		return nil, fmt.Errorf("ovh get %s: %w", zone, err)
	}

	// First pass: collect the relative names of address records we own (those
	// whose "_shuttle-owner.<name>" TXT carries our marker).
	owned := map[string]bool{}
	for _, rec := range recs {
		txt, ok := rec.(libdns.TXT)
		if !ok || txt.Text != ownerTXTValue || !strings.HasPrefix(txt.Name, ownerTXTPrefix) {
			continue
		}
		owned[strings.TrimPrefix(txt.Name, ownerTXTPrefix)] = true
	}

	// Second pass: emit the owned A/AAAA/CNAME records.
	var out []Record
	for _, rec := range recs {
		switch v := rec.(type) {
		case libdns.Address:
			if !owned[v.Name] {
				continue
			}
			typ := "A"
			if v.IP.Is6() {
				typ = "AAAA"
			}
			out = append(out, Record{Name: fqdnTrim(libdns.AbsoluteName(v.Name, z)), Type: typ, Value: v.IP.String()})
		case libdns.CNAME:
			if !owned[v.Name] {
				continue
			}
			out = append(out, Record{Name: fqdnTrim(libdns.AbsoluteName(v.Name, z)), Type: "CNAME", Value: fqdnTrim(v.Target)})
		}
	}
	return out, nil
}

// recordFor builds the libdns record for r within absolute zone z: an A/AAAA
// Address for an IP value, or a CNAME for a hostname value.
func recordFor(z string, r Record) (libdns.Record, error) {
	name := libdns.RelativeName(fqdnDot(r.Name), z)
	if r.Type == "CNAME" {
		return libdns.CNAME{Name: name, Target: fqdnDot(r.Value)}, nil
	}
	ip, err := netip.ParseAddr(r.Value)
	if err != nil {
		return nil, fmt.Errorf("dns: record %s has invalid IP %q: %w", r.Name, r.Value, err)
	}
	return libdns.Address{Name: name, IP: ip}, nil
}

// ownerTXTRecord builds the owner-TXT marker for fqdn within absolute zone z.
func ownerTXTRecord(z, fqdn string) libdns.TXT {
	return libdns.TXT{Name: libdns.RelativeName(fqdnDot(ownerTXTName(fqdn)), z), Text: ownerTXTValue}
}

func zoneFQDN(zone string) string { return fqdnDot(zone) }

func fqdnDot(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

func fqdnTrim(s string) string { return strings.TrimSuffix(s, ".") }

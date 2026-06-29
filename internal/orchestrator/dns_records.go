package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/dns"
	"github.com/neikow/shuttle/internal/ledger"
)

// DNSReconciler creates, updates, and deletes the A/AAAA records Shuttle manages
// for services' domains (dns.yml zones), and heals out-of-band drift. It ticks
// like the DriftReconciler but does no git sync of its own — it reads the working
// copy the DriftReconciler keeps current.
//
// Source of truth is threefold: the repo says what records *should* exist; the
// provider (via a Manager) says what *does* exist among records Shuttle owns
// (those carrying the owner TXT); and the dns_records ledger mirrors what Shuttle
// has applied. Shuttle only ever creates/updates/deletes records it owns —
// foreign records are never touched.
type DNSReconciler struct {
	syncer   *GitSyncer
	store    *ledger.Store
	interval time.Duration
	bus      *EventBus

	// newManager builds a record Manager for a provider; overridable in tests.
	newManager func(providerType, endpoint string, creds map[string]string) (dns.Manager, error)
}

func NewDNSReconciler(syncer *GitSyncer, store *ledger.Store, interval time.Duration) *DNSReconciler {
	return &DNSReconciler{syncer: syncer, store: store, interval: interval, newManager: dns.NewManager}
}

// SetEventBus attaches the event bus DNS events are published to. Call before Run.
func (d *DNSReconciler) SetEventBus(b *EventBus) { d.bus = b }

// Run ticks until ctx is cancelled.
func (d *DNSReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

// desiredRecord is one record the repo says should exist, with the zone/provider
// that governs it.
type desiredRecord struct {
	rec      dns.Record
	zone     config.DNSZone
	provider config.DNSProvider
	service  string
	host     string
}

func (d *DNSReconciler) tick(ctx context.Context) {
	repo, err := config.Load(d.syncer.LocalDir())
	if err != nil {
		slog.Error("dns reconcile: load config", "err", err)
		return
	}
	if repo.DNS == nil || len(repo.DNS.Zones) == 0 {
		return
	}

	desired, problems := buildDesiredRecords(repo)
	for _, p := range problems {
		slog.Warn("dns reconcile", "problem", p)
	}
	byZone := map[string][]desiredRecord{}
	for _, dr := range desired {
		byZone[dr.zone.Domain] = append(byZone[dr.zone.Domain], dr)
	}

	hosts := hostMap(repo)
	serial := time.Now().Unix()
	credCache := map[string]map[string]string{}
	sidecarByHost := map[string]*sidecarZones{}
	for i := range repo.DNS.Zones {
		z := repo.DNS.Zones[i]
		prov := repo.DNS.ProviderByName(z.Provider)

		// Sidecar zones are pushed as full zone files to the host's CoreDNS, not
		// reconciled record-by-record through a Manager.
		if prov != nil && prov.Type == "sidecar" {
			nsIP := hosts[prov.Host].Address(z.Address)
			zf := renderZoneFile(z.Domain, recordsOf(byZone[z.Domain]), nsIP, serial)
			sz := sidecarByHost[prov.Host]
			if sz == nil {
				sz = &sidecarZones{port: prov.SidecarPort()}
				sidecarByHost[prov.Host] = sz
			}
			sz.files = append(sz.files, &shuttlev1.DNSZoneFile{Origin: z.Domain, Zonefile: zf})
			continue
		}

		if err := d.reconcileZone(ctx, z, repo.DNS, byZone[z.Domain], credCache); err != nil {
			slog.Error("dns reconcile zone", "zone", z.Domain, "err", err)
		}
	}
	d.dispatchSidecar(sidecarByHost)
}

// buildDesiredRecords derives the records the repo wants: for each managed domain
// of each non-external service, an A/AAAA record pointing at the service's host
// address for the matched zone. problems collects skips (missing host address,
// unparseable IP) for the operator to see in logs / check / plan.
func buildDesiredRecords(repo *config.Repo) (records []desiredRecord, problems []string) {
	hosts := make(map[string]config.Host, len(repo.Hosts))
	for _, h := range repo.Hosts {
		hosts[h.Name] = h
	}
	for _, svc := range repo.Services {
		if svc.IsExternal() || svc.Host == "" || len(svc.Domains) == 0 {
			continue
		}
		host, ok := hosts[svc.Host]
		if !ok {
			continue
		}
		for _, domain := range svc.Domains {
			zone := repo.DNS.ZoneFor(domain)
			if zone == nil {
				continue // no zone manages this domain
			}
			ip := host.Address(zone.Address)
			if ip == "" {
				problems = append(problems, fmt.Sprintf("service %q domain %q: host %q has no %q address", svc.Name, domain, svc.Host, addrLabel(zone.Address)))
				continue
			}
			typ, perr := recordType(ip)
			if perr != "" {
				problems = append(problems, fmt.Sprintf("service %q domain %q: %s", svc.Name, domain, perr))
				continue
			}
			prov := repo.DNS.ProviderByName(zone.Provider)
			if prov == nil {
				continue // validated at load
			}
			records = append(records, desiredRecord{
				rec:      dns.Record{Name: domain, Type: typ, Value: ip},
				zone:     *zone,
				provider: *prov,
				service:  svc.Name,
				host:     svc.Host,
			})
		}
	}
	return records, problems
}

func (d *DNSReconciler) reconcileZone(ctx context.Context, z config.DNSZone, dnsCfg *config.DNSConfig, desired []desiredRecord, credCache map[string]map[string]string) error {
	prov := dnsCfg.ProviderByName(z.Provider)
	if prov == nil {
		return fmt.Errorf("unknown provider %q", z.Provider)
	}

	// The manual provider creates nothing — surface the intended records so the
	// operator can create them by hand.
	if prov.Type == "manual" {
		for _, dr := range desired {
			slog.Info("dns manual record (create it yourself)",
				"zone", z.Domain, "name", dr.rec.Name, "type", dr.rec.Type, "value", dr.rec.Value)
		}
		return nil
	}

	creds, ok := credCache[prov.Name]
	if !ok {
		c, err := d.syncer.resolveDNSCreds(ctx, *prov)
		if err != nil {
			return fmt.Errorf("resolve creds: %w", err)
		}
		creds, credCache[prov.Name] = c, c
	}
	mgr, err := d.newManager(prov.Type, prov.Endpoint, creds)
	if err != nil {
		return err
	}

	owned, err := mgr.Owned(ctx, z.Domain)
	if err != nil && !errors.Is(err, dns.ErrUnsupported) {
		return fmt.Errorf("list owned: %w", err)
	}
	ownedByKey := make(map[string]dns.Record, len(owned))
	for _, o := range owned {
		ownedByKey[recKey(o.Name, o.Type)] = o
	}

	desiredByKey := make(map[string]bool, len(desired))
	for _, dr := range desired {
		key := recKey(dr.rec.Name, dr.rec.Type)
		desiredByKey[key] = true
		cur, exists := ownedByKey[key]
		switch {
		case !exists:
			if err := mgr.Ensure(ctx, z.Domain, dr.rec); err != nil {
				slog.Error("dns ensure", "name", dr.rec.Name, "err", err)
				continue
			}
			d.recordLedger(ctx, dr)
			d.bus.Publish(Event{
				Type: EventDNSRecordSet, Service: dr.service, Host: dr.host,
				Message: fmt.Sprintf("created %s %s -> %s", dr.rec.Type, dr.rec.Name, dr.rec.Value),
				Detail:  recDetail(dr),
			})
		case cur.Value != dr.rec.Value:
			if err := mgr.Ensure(ctx, z.Domain, dr.rec); err != nil {
				slog.Error("dns heal", "name", dr.rec.Name, "err", err)
				continue
			}
			d.recordLedger(ctx, dr)
			d.bus.Publish(Event{
				Type: EventDNSDriftHealed, Service: dr.service, Host: dr.host,
				Message: fmt.Sprintf("healed %s %s: %s -> %s", dr.rec.Type, dr.rec.Name, cur.Value, dr.rec.Value),
				Detail:  map[string]string{"fqdn": dr.rec.Name, "type": dr.rec.Type, "old": cur.Value, "new": dr.rec.Value, "provider": prov.Name},
			})
		default:
			d.recordLedger(ctx, dr) // already correct; keep the ledger mirror current
		}
	}

	// Delete records Shuttle owns that are no longer desired.
	for key, o := range ownedByKey {
		if desiredByKey[key] {
			continue
		}
		if err := mgr.Remove(ctx, z.Domain, o); err != nil {
			slog.Error("dns remove", "name", o.Name, "err", err)
			continue
		}
		if err := d.store.DeleteDNSRecord(ctx, o.Name, o.Type); err != nil {
			slog.Error("dns ledger delete", "name", o.Name, "err", err)
		}
		d.bus.Publish(Event{
			Type: EventDNSRecordRemoved,
			Message: fmt.Sprintf("removed %s %s", o.Type, o.Name),
			Detail:  map[string]string{"fqdn": o.Name, "type": o.Type, "provider": prov.Name},
		})
	}
	return nil
}

func (d *DNSReconciler) recordLedger(ctx context.Context, dr desiredRecord) {
	if err := d.store.UpsertDNSRecord(ctx, ledger.DNSRecord{
		FQDN: dr.rec.Name, Type: dr.rec.Type, Value: dr.rec.Value,
		Provider: dr.provider.Name, Zone: dr.zone.Domain, Service: dr.service, Host: dr.host,
	}); err != nil {
		slog.Error("dns ledger upsert", "name", dr.rec.Name, "err", err)
	}
}

func recKey(name, recordType string) string { return name + "/" + recordType }

func recDetail(dr desiredRecord) map[string]string {
	return map[string]string{
		"fqdn": dr.rec.Name, "type": dr.rec.Type,
		"value": dr.rec.Value, "provider": dr.provider.Name,
	}
}

// recordType classifies a host address value: an IP literal yields A/AAAA; a
// hostname yields CNAME; anything else is a problem.
func recordType(value string) (recordType, problem string) {
	if a, err := netip.ParseAddr(value); err == nil {
		if a.Is6() {
			return "AAAA", ""
		}
		return "A", ""
	}
	// Not an IP — accept a dotted hostname as a CNAME target.
	if strings.Contains(value, ".") && !strings.ContainsAny(value, " /:") {
		return "CNAME", ""
	}
	return "", fmt.Sprintf("host address %q is neither an IP nor a hostname", value)
}

func addrLabel(label string) string {
	if label == "" {
		return config.DefaultAddressLabel
	}
	return label
}

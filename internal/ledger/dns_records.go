package ledger

import (
	"context"
	"time"
)

// DNSRecord is one row of the dns_records table: a DNS record Shuttle manages
// (owns) for a service. Keyed by (FQDN, Type).
type DNSRecord struct {
	FQDN      string    `json:"fqdn"`
	Type      string    `json:"type"` // A | AAAA
	Value     string    `json:"value"`
	Provider  string    `json:"provider"`
	Zone      string    `json:"zone"`
	Service   string    `json:"service"`
	Host      string    `json:"host"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpsertDNSRecord records (or updates) ownership of a managed record. Called by
// the DNSReconciler after the provider confirms the record is in place.
func (s *Store) UpsertDNSRecord(ctx context.Context, r DNSRecord) error {
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dns_records (fqdn, type, value, provider, zone, service, host, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(fqdn, type) DO UPDATE SET
		   value      = excluded.value,
		   provider   = excluded.provider,
		   zone       = excluded.zone,
		   service    = excluded.service,
		   host       = excluded.host,
		   updated_at = excluded.updated_at`,
		r.FQDN, r.Type, r.Value, r.Provider, r.Zone, r.Service, r.Host, r.UpdatedAt.UnixMilli(),
	)
	return err
}

// ListDNSRecords returns every managed record, ordered by name then type.
func (s *Store) ListDNSRecords(ctx context.Context) ([]DNSRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fqdn, type, value, provider, zone, service, host, updated_at
		   FROM dns_records ORDER BY fqdn, type`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []DNSRecord
	for rows.Next() {
		var r DNSRecord
		var updated int64
		if err := rows.Scan(&r.FQDN, &r.Type, &r.Value, &r.Provider, &r.Zone, &r.Service, &r.Host, &updated); err != nil {
			return nil, err
		}
		r.UpdatedAt = time.UnixMilli(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteDNSRecord drops a managed record from the catalog. Called after the
// provider record (and its owner TXT) have been removed.
func (s *Store) DeleteDNSRecord(ctx context.Context, fqdn, recordType string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM dns_records WHERE fqdn = ? AND type = ?`, fqdn, recordType)
	return err
}

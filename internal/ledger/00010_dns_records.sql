-- +goose Up

-- dns_records is the catalog of DNS A/AAAA records Shuttle manages on behalf of
-- services (see dns.yml zones). It is the orchestrator's record of *what it
-- owns*: the DNSReconciler diffs desired records (service domains x zone x host
-- address) against this table to decide creates/updates/deletes, and pairs it
-- with a provider-side owner TXT so out-of-band drift can be detected and healed.
-- Shuttle only ever modifies or deletes records present here (and confirmed by
-- the owner TXT) — foreign records are never touched.
--
-- Mutable lifecycle state (like service_lifecycle), distinct from the append-only
-- deploys ledger. Keyed by (fqdn, type): one A and/or AAAA per name. A row is
-- removed when its service/domain leaves the repo and the provider record is
-- deleted.
CREATE TABLE IF NOT EXISTS dns_records (
    fqdn       TEXT    NOT NULL,
    type       TEXT    NOT NULL,           -- A | AAAA
    value      TEXT    NOT NULL,           -- the target IP
    provider   TEXT    NOT NULL,           -- dns.yml provider name that manages it
    zone       TEXT    NOT NULL,           -- matched zone domain
    service    TEXT    NOT NULL,           -- owning service
    host       TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (fqdn, type)
);

CREATE INDEX IF NOT EXISTS idx_dns_records_service ON dns_records(service);

-- +goose Down
DROP TABLE IF EXISTS dns_records;

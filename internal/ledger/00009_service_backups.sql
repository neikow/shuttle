-- +goose Up

-- service_backups records every service-data backup attempt (volume tar/restic
-- snapshot or postgres dump), keyed by backup_id. It is the catalog the control
-- plane reads to list a service's restore points, decide when a scheduled backup
-- is next due, and resolve the snapshot a restore should target. Distinct from
-- the append-only deploys ledger (which records deploy *state*, not data
-- backups) and from backup.go's VACUUM snapshot of the ledger itself.
--
-- A row starts at status='pending' when the orchestrator dispatches the backup
-- and is updated to success/failed when the agent reports back; snapshot_id and
-- size_bytes are filled on success. Append-only per attempt — never deleted by
-- the orchestrator (the restic/local store governs its own retention).
CREATE TABLE IF NOT EXISTS service_backups (
    backup_id    TEXT    NOT NULL PRIMARY KEY,
    service      TEXT    NOT NULL,
    host         TEXT    NOT NULL,
    engine       TEXT    NOT NULL,
    store        TEXT    NOT NULL,
    target       TEXT    NOT NULL DEFAULT '',
    snapshot_id  TEXT    NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    status       TEXT    NOT NULL,
    triggered_by TEXT    NOT NULL DEFAULT '',
    error        TEXT    NOT NULL DEFAULT '',
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_service_backups_service
    ON service_backups(service, started_at);

-- +goose Down
DROP TABLE IF EXISTS service_backups;

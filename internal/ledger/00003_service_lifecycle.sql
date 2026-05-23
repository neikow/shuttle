-- +goose Up

-- service_lifecycle tracks each service the orchestrator manages, so it can
-- detect when a service is removed from the IaC repo and drive its teardown.
-- The deploys table is append-only and cannot express "no longer desired", and
-- a removed service's delete_volumes policy is gone from the repo — both are
-- recorded here while the service is present.
--
-- Lifecycle: present=1 while the service is in the repo. When it disappears,
-- present flips to 0 and removed_at is stamped; containers_removed_at marks the
-- teardown dispatch. The volume columns are populated by the volume-purge
-- feature (manual default keeps volumes until an explicit prune).
CREATE TABLE IF NOT EXISTS service_lifecycle (
    service               TEXT    PRIMARY KEY,
    host                  TEXT    NOT NULL,
    delete_volumes        TEXT    NOT NULL DEFAULT 'manual',
    present               INTEGER NOT NULL DEFAULT 1,
    removed_at            INTEGER,
    containers_removed_at INTEGER,
    volumes_purge_after   INTEGER,
    volumes_purged_at     INTEGER,
    updated_at            INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_service_lifecycle_present ON service_lifecycle(present);

-- +goose Down

DROP INDEX IF EXISTS idx_service_lifecycle_present;
DROP TABLE IF EXISTS service_lifecycle;

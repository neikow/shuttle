-- +goose Up

CREATE TABLE IF NOT EXISTS deploys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    deploy_id    TEXT    UNIQUE NOT NULL,
    service      TEXT    NOT NULL,
    host         TEXT    NOT NULL,
    sha          TEXT    NOT NULL,
    status       TEXT    NOT NULL CHECK(status IN ('pending','running','success','failed','rolled_back')),
    triggered_by TEXT    NOT NULL CHECK(triggered_by IN ('webhook','manual','rollback')),
    webhook_nonce TEXT,
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_deploys_service_started ON deploys(service, started_at DESC);

CREATE TABLE IF NOT EXISTS webhook_nonces (
    nonce   TEXT    PRIMARY KEY,
    seen_at INTEGER NOT NULL
);

-- +goose Down

DROP INDEX IF EXISTS idx_deploys_service_started;
DROP TABLE IF EXISTS deploys;
DROP TABLE IF EXISTS webhook_nonces;

-- +goose Up

-- audit_log is an append-only record of every control-plane mutation (deploy,
-- rollback, prune, enrollment, webhook CRUD). It captures *who* did *what* —
-- the actor, the action, its target, the source IP, and the outcome — so an
-- operator can answer "who deployed this / who minted that agent token". It is
-- separate from the deploys table (which records deploy *state*, not actor
-- identity) and is never mutated after insert.
CREATE TABLE IF NOT EXISTS audit_log (
    id         TEXT    PRIMARY KEY,
    ts         INTEGER NOT NULL,
    actor      TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    source_ip  TEXT    NOT NULL DEFAULT '',
    result     TEXT    NOT NULL,
    detail     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log(ts);

-- +goose Down
DROP TABLE IF EXISTS audit_log;

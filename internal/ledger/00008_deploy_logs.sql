-- +goose Up

-- deploy_logs stores the captured output of each deploy/rollback attempt, keyed
-- by deploy_id and ordered by seq. The agent streams a single final
-- DeployResponse carrying the full log; the orchestrator persists it here so an
-- operator can inspect *why* a deploy succeeded or failed from the control plane
-- (UI / `GET /deploys/{id}/logs`) instead of grepping agent host logs. Append-
-- only and best-effort: a failed log write never gates the deploy result.
CREATE TABLE IF NOT EXISTS deploy_logs (
    deploy_id  TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    stream     TEXT    NOT NULL DEFAULT 'stdout',
    text       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (deploy_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_deploy_logs_deploy ON deploy_logs(deploy_id);

-- +goose Down
DROP TABLE IF EXISTS deploy_logs;

-- +goose Up

CREATE TABLE IF NOT EXISTS repo_webhooks (
    id         TEXT    PRIMARY KEY,
    service    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS repo_webhooks;

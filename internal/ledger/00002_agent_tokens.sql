-- +goose Up

-- Long-lived, host-scoped agent enrollment tokens. Only the SHA-256 hash of the
-- token is stored; the plaintext is shown once at enrollment time.
CREATE TABLE IF NOT EXISTS agent_tokens (
    id         TEXT    PRIMARY KEY,
    host       TEXT    NOT NULL,
    token_hash TEXT    UNIQUE NOT NULL,
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_agent_tokens_host ON agent_tokens(host);

-- +goose Down

DROP INDEX IF EXISTS idx_agent_tokens_host;
DROP TABLE IF EXISTS agent_tokens;

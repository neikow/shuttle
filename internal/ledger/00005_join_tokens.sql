-- +goose Up

-- join_tokens are short-lived, single-use credentials handed to an operator by
-- `shuttle enroll` and redeemed once on the target host by `shuttle agent join`,
-- which exchanges them for a long-lived host-scoped agent token. Storing only
-- the hash mirrors agent_tokens; expires_at bounds the leak window and used_at
-- enforces single use.
CREATE TABLE IF NOT EXISTS join_tokens (
    id          TEXT    PRIMARY KEY,
    host        TEXT    NOT NULL,
    token_hash  TEXT    UNIQUE NOT NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    used_at     INTEGER
);

CREATE INDEX IF NOT EXISTS idx_join_tokens_hash ON join_tokens(token_hash);

-- +goose Down
DROP TABLE IF EXISTS join_tokens;

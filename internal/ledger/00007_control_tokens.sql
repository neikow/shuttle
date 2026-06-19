-- +goose Up

-- control_tokens are named, role-scoped HTTP control-plane bearer tokens. They
-- add role-based access control (read / deploy / admin) on top of the single
-- static bearer_token (which remains the bootstrap admin). Only the SHA-256
-- hash is stored, mirroring agent_tokens; role gates which endpoints the token
-- may call; name gives the token an identity that flows into the audit log as
-- the actor. revoked_at retires a token without deleting its history.
CREATE TABLE IF NOT EXISTS control_tokens (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    token_hash  TEXT    UNIQUE NOT NULL,
    role        TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    revoked_at  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_control_tokens_hash ON control_tokens(token_hash);

-- +goose Down
DROP TABLE IF EXISTS control_tokens;

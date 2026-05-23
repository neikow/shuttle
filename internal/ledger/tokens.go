package ledger

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AgentToken is an enrollment token record. The plaintext token is never stored
// — only its hash. Host scopes the token to a single managed host.
type AgentToken struct {
	ID        string
	Host      string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// CreateAgentToken records a new token's hash, scoped to host, and returns the
// generated record ID.
func (s *Store) CreateAgentToken(ctx context.Context, id, host, tokenHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tokens (id, host, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, host, tokenHash, time.Now().UnixMilli(),
	)
	return err
}

// AgentTokenHost returns the host a token hash is scoped to, if the token exists
// and has not been revoked.
func (s *Store) AgentTokenHost(ctx context.Context, tokenHash string) (host string, ok bool, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT host FROM agent_tokens WHERE token_hash = ? AND revoked_at IS NULL`,
		tokenHash,
	)
	switch err := row.Scan(&host); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	default:
		return host, true, nil
	}
}

// RevokeAgentToken marks a token revoked by ID. Revoking an unknown or
// already-revoked token is a no-op.
func (s *Store) RevokeAgentToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UnixMilli(), id,
	)
	return err
}

// ListAgentTokens returns all token records (newest first), without the hash.
func (s *Store) ListAgentTokens(ctx context.Context) ([]AgentToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, host, created_at, revoked_at FROM agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AgentToken
	for rows.Next() {
		var t AgentToken
		var createdMs int64
		var revokedMs sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Host, &createdMs, &revokedMs); err != nil {
			return nil, err
		}
		t.CreatedAt = time.UnixMilli(createdMs)
		if revokedMs.Valid {
			r := time.UnixMilli(revokedMs.Int64)
			t.RevokedAt = &r
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

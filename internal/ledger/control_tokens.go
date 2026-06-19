package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ControlToken is a named, role-scoped control-plane bearer token. The plaintext
// token is never stored — only its hash.
type ControlToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// ErrControlTokenNotFound is returned when a control token ID does not exist.
type ErrControlTokenNotFound struct{ ID string }

func (e ErrControlTokenNotFound) Error() string { return "control token not found: " + e.ID }

// CreateControlToken records a new token's hash with a name and role, returning
// the row's creation time.
func (s *Store) CreateControlToken(ctx context.Context, id, name, tokenHash, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO control_tokens (id, name, token_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, tokenHash, role, time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("insert control token: %w", err)
	}
	return nil
}

// LookupControlToken returns the name and role bound to a token hash, when the
// token exists and has not been revoked. ok is false for unknown or revoked
// tokens (the two are indistinguishable to the caller, by design).
func (s *Store) LookupControlToken(ctx context.Context, tokenHash string) (name, role string, ok bool, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, role FROM control_tokens WHERE token_hash = ? AND revoked_at IS NULL`,
		tokenHash,
	)
	switch err := row.Scan(&name, &role); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", false, nil
	case err != nil:
		return "", "", false, fmt.Errorf("lookup control token: %w", err)
	default:
		return name, role, true, nil
	}
}

// RevokeControlToken marks a token revoked by ID. Returns ErrControlTokenNotFound
// if no such (non-revoked) token exists.
func (s *Store) RevokeControlToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE control_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("revoke control token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrControlTokenNotFound{ID: id}
	}
	return nil
}

// ListControlTokens returns all token records (newest first), without the hash.
func (s *Store) ListControlTokens(ctx context.Context) ([]ControlToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, role, created_at, revoked_at FROM control_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list control tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ControlToken
	for rows.Next() {
		var t ControlToken
		var createdMs int64
		var revokedMs sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &createdMs, &revokedMs); err != nil {
			return nil, fmt.Errorf("scan control token: %w", err)
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

package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrJoinTokenInvalid is returned when a join token is unknown, already used, or
// expired. It is deliberately undifferentiated so a redeemer cannot distinguish
// "wrong token" from "expired token" by the error.
var ErrJoinTokenInvalid = errors.New("join token invalid, expired, or already used")

// CreateJoinToken stores a single-use join token hash bound to host, expiring at
// exp. The plaintext is shown once by `shuttle enroll`; only the hash is kept.
func (s *Store) CreateJoinToken(ctx context.Context, id, host, tokenHash string, exp time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO join_tokens (id, host, token_hash, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, host, tokenHash, time.Now().UnixMilli(), exp.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("create join token: %w", err)
	}
	return nil
}

// RedeemJoinToken atomically claims an unexpired, unused join token by its hash
// and returns the host it was minted for. The claim is a single UPDATE guarded
// on used_at IS NULL AND expires_at > now, so two concurrent redeems can never
// both succeed. Returns ErrJoinTokenInvalid if no row matched.
func (s *Store) RedeemJoinToken(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var host string
	err := s.db.QueryRowContext(ctx,
		`UPDATE join_tokens SET used_at = ?
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
		 RETURNING host`,
		now.UnixMilli(), tokenHash, now.UnixMilli(),
	).Scan(&host)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrJoinTokenInvalid
	}
	if err != nil {
		return "", fmt.Errorf("redeem join token: %w", err)
	}
	return host, nil
}

// PurgeExpiredJoinTokens deletes join tokens whose expiry has passed. Used and
// unused alike are removed once expired; redemption is irreversible so a used
// row carries no further value. Returns the number deleted.
func (s *Store) PurgeExpiredJoinTokens(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM join_tokens WHERE expires_at <= ?`, now.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("purge expired join tokens: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

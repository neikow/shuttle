package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// AuditEntry is one immutable record in the audit_log: who (Actor) did what
// (Action) to which thing (Target), from where (SourceIP), and how it turned
// out (Result, with optional Detail). At is the wall-clock time of the action.
type AuditEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target,omitempty"`
	SourceIP string    `json:"source_ip,omitempty"`
	Result   string    `json:"result"`
	Detail   string    `json:"detail,omitempty"`
}

// RecordAudit appends one entry to the audit log, generating a random 16-byte
// hex ID and defaulting At to now when zero. It returns the generated ID. The
// log is append-only — entries are never updated or deleted by the application.
func (s *Store) RecordAudit(ctx context.Context, e AuditEntry) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate audit id: %w", err)
	}
	id := hex.EncodeToString(buf[:])
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (id, ts, actor, action, target, source_ip, result, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, e.At.UnixMilli(), e.Actor, e.Action, e.Target, e.SourceIP, e.Result, e.Detail,
	)
	if err != nil {
		return "", fmt.Errorf("insert audit entry: %w", err)
	}
	return id, nil
}

// ListAudit returns the most recent audit entries, newest first. A non-empty
// action filters to that action only; limit caps the row count.
func (s *Store) ListAudit(ctx context.Context, action string, limit int) ([]AuditEntry, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if action == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, ts, actor, action, target, source_ip, result, detail
			 FROM audit_log ORDER BY ts DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, ts, actor, action, target, source_ip, result, detail
			 FROM audit_log WHERE action = ? ORDER BY ts DESC LIMIT ?`, action, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ms int64
		if err := rows.Scan(&e.ID, &ms, &e.Actor, &e.Action, &e.Target, &e.SourceIP, &e.Result, &e.Detail); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		e.At = time.UnixMilli(ms)
		out = append(out, e)
	}
	return out, rows.Err()
}

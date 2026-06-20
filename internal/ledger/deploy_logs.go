package ledger

import (
	"context"
	"fmt"
	"time"
)

// DeployLog is one captured line of a deploy/rollback's output. Stream is
// "stdout" or "stderr"; At is when the agent emitted the line.
type DeployLog struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

// RecordDeployLogs appends the captured log lines for one deploy, ordered by
// their position in logs. It is a no-op for an empty slice. Lines are written in
// a single transaction so a deploy's log is all-or-nothing; the caller treats a
// failure as best-effort (logs must never gate the deploy result).
func (s *Store) RecordDeployLogs(ctx context.Context, deployID string, logs []DeployLog) error {
	if deployID == "" || len(logs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deploy logs tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO deploy_logs (deploy_id, seq, ts, stream, text)
		 VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare deploy logs insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i, l := range logs {
		stream := l.Stream
		if stream == "" {
			stream = "stdout"
		}
		ts := l.At
		if ts.IsZero() {
			ts = time.Now()
		}
		if _, err := stmt.ExecContext(ctx, deployID, i, ts.UnixMilli(), stream, l.Text); err != nil {
			return fmt.Errorf("insert deploy log line: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deploy logs: %w", err)
	}
	return nil
}

// DeployLogs returns the captured log lines for one deploy in emission order.
// An unknown deploy_id (or one with no captured output) yields an empty slice.
func (s *Store) DeployLogs(ctx context.Context, deployID string) ([]DeployLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, stream, text FROM deploy_logs WHERE deploy_id = ? ORDER BY seq ASC`,
		deployID)
	if err != nil {
		return nil, fmt.Errorf("query deploy logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []DeployLog{}
	for rows.Next() {
		var (
			l  DeployLog
			ms int64
		)
		if err := rows.Scan(&ms, &l.Stream, &l.Text); err != nil {
			return nil, fmt.Errorf("scan deploy log line: %w", err)
		}
		l.At = time.UnixMilli(ms)
		out = append(out, l)
	}
	return out, rows.Err()
}

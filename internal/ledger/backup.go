package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DBFileName is the ledger filename within a data directory. The orchestrator
// opens <data_dir>/<DBFileName>; backup/restore operate on the same path.
const DBFileName = "shuttle.db"

// BackupTo writes a consistent snapshot of the ledger to dest using SQLite's
// VACUUM INTO. dest must not already exist. The snapshot is a plain (non-WAL)
// database file safe to copy or archive, and can be opened directly or handed
// to RestoreInto. Safe to run against a live ledger: VACUUM INTO takes a read
// transaction, so it captures a consistent point-in-time copy without stopping
// the orchestrator.
func (s *Store) BackupTo(ctx context.Context, dest string) error {
	if dest == "" {
		return fmt.Errorf("backup destination required")
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists", dest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dest, err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}

// Verify opens path as a SQLite database and checks it looks like a shuttle
// ledger (the deploys table is present and queryable). Used by RestoreInto to
// refuse a bogus or corrupt file before it overwrites the live ledger.
func Verify(path string) error {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM deploys").Scan(&n); err != nil {
		return fmt.Errorf("%s is not a valid shuttle ledger: %w", path, err)
	}
	return nil
}

// RestoreInto validates the backup at src and installs it as the ledger in
// dataDir, replacing any existing ledger. The orchestrator MUST NOT be running
// against dataDir during a restore. Stale WAL/SHM sidecars are removed so the
// restored snapshot is authoritative rather than shadowed by an old WAL.
func RestoreInto(src, dataDir string) error {
	if err := Verify(src); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	dest := filepath.Join(dataDir, DBFileName)
	if err := copyFileAtomic(src, dest); err != nil {
		return err
	}
	for _, sidecar := range []string{dest + "-wal", dest + "-shm"} {
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", sidecar, err)
		}
	}
	return nil
}

// copyFileAtomic copies src to dest by writing a temp file in dest's directory
// and renaming it into place, so a crash mid-copy never leaves a half-written
// ledger at dest.
func copyFileAtomic(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".shuttle-restore-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

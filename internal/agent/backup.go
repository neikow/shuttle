package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Backup engines and stores (mirror config.BackupEngine*/BackupStore* without
// importing the config package).
const (
	backupEngineVolume   = "volume"
	backupEnginePostgres = "postgres"
	backupStoreLocal     = "local"
	backupStoreRestic    = "restic"
)

// Helper container images. Volumes are tarred with a tiny busybox/alpine; restic
// operations run the official restic image.
const (
	tarImage    = "alpine:3"
	resticImage = "restic/restic:latest"
)

// Container mount points used by the helper containers. Kept fixed so backup and
// restore agree on the snapshot's internal paths.
const (
	mountVol   = "/vol"   // a source docker volume (read-only) during capture
	mountStage = "/stage" // the staging directory holding tar/sql artifacts
	mountRepo  = "/repo"  // a local restic repository
)

// BackupRetention bounds restic snapshot pruning (mirrors config.BackupRetention
// / the proto BackupRetention).
type BackupRetention struct {
	KeepLast    int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
}

// BackupParams are the inputs for a backup. The service's compose workspace is
// already on disk in WorkDir; the agent renders nothing.
type BackupParams struct {
	BackupID  string
	Service   string
	Engine    string            // "volume" | "postgres"
	Store     string            // "local" | "restic"
	Target    string            // local directory or restic repository
	Env       map[string]string // backend creds forwarded to helper containers
	WorkDir   string            // the service's compose workspace
	Volumes   []string          // engine=volume: restrict to these compose volume keys
	DBService string            // engine=postgres: compose service of the DB container
	DBUser    string
	DBName    string // empty => pg_dumpall
	Retention BackupRetention
}

// RestoreParams are the inputs for a restore. SnapshotID selects which backup to
// restore (a restic snapshot id, or — for the local store — the backup id whose
// directory holds the artifacts).
type RestoreParams struct {
	OperationID string
	Service     string
	Engine      string
	Store       string
	Target      string
	SnapshotID  string
	Env         map[string]string
	WorkDir     string
	DBService   string
	DBUser      string
	DBName      string
}

// BackupOutcome is the terminal result of a backup or restore. SnapshotID and
// SizeBytes are meaningful only for a successful backup.
type BackupOutcome struct {
	SnapshotID string
	SizeBytes  int64
	Failed     bool
	Err        string
}

// Backup captures a service's data and stores it. It returns a log channel (the
// command output, streamed live) and a single-value outcome channel delivered
// once the operation finishes (after the log channel closes).
func (d *ComposeDriver) Backup(ctx context.Context, p BackupParams) (<-chan LogLine, <-chan BackupOutcome, error) {
	logs := make(chan LogLine, 64)
	done := make(chan BackupOutcome, 1)
	go func() {
		defer close(logs)
		out := d.runBackup(ctx, p, func(l LogLine) { logs <- l })
		done <- out
		close(done)
	}()
	return logs, done, nil
}

// Restore reverses a backup into the service. It always stops the service's
// containers, applies the data, and starts them again (cold restore).
func (d *ComposeDriver) Restore(ctx context.Context, p RestoreParams) (<-chan LogLine, <-chan BackupOutcome, error) {
	logs := make(chan LogLine, 64)
	done := make(chan BackupOutcome, 1)
	go func() {
		defer close(logs)
		out := d.runRestore(ctx, p, func(l LogLine) { logs <- l })
		done <- out
		close(done)
	}()
	return logs, done, nil
}

// runBackup performs the capture+store synchronously, emitting log lines via
// emit, and returns the outcome.
func (d *ComposeDriver) runBackup(ctx context.Context, p BackupParams, emit func(LogLine)) BackupOutcome {
	composePath := filepath.Join(p.WorkDir, "docker-compose.yml")
	envFile := filepath.Join(p.WorkDir, ".env")
	if _, err := os.Stat(composePath); err != nil {
		return failOutcome(emit, fmt.Errorf("no compose workspace for %q (deploy it first): %w", p.Service, err))
	}

	// Stage directory: for the local store, write artifacts straight into their
	// final home so there is nothing to copy; for restic, a temp dir the snapshot
	// is taken from and then discarded.
	var stage string
	if p.Store == backupStoreLocal {
		stage = filepath.Join(p.Target, p.Service, p.BackupID)
	} else {
		tmp, err := os.MkdirTemp("", "shuttle-backup-")
		if err != nil {
			return failOutcome(emit, fmt.Errorf("stage dir: %w", err))
		}
		stage = tmp
		defer func() { _ = os.RemoveAll(tmp) }()
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return failOutcome(emit, fmt.Errorf("mkdir stage: %w", err))
	}

	// Capture artifacts into the stage directory.
	switch p.Engine {
	case backupEngineVolume:
		if err := d.captureVolumes(ctx, p, composePath, envFile, stage, emit); err != nil {
			return failOutcome(emit, err)
		}
	case backupEnginePostgres:
		if err := d.capturePostgres(ctx, p, composePath, envFile, stage, emit); err != nil {
			return failOutcome(emit, err)
		}
	default:
		return failOutcome(emit, fmt.Errorf("unknown backup engine %q", p.Engine))
	}

	size, err := dirSize(stage)
	if err != nil {
		return failOutcome(emit, fmt.Errorf("size stage: %w", err))
	}

	// Store: local artifacts already sit at their final path; restic snapshots
	// the stage directory.
	snapshotID := p.BackupID
	if p.Store == backupStoreRestic {
		sid, rbytes, err := d.resticBackup(ctx, p, stage, emit)
		if err != nil {
			return failOutcome(emit, err)
		}
		snapshotID = sid
		if rbytes > 0 {
			size = rbytes
		}
	}
	emit(infoLine(fmt.Sprintf("[shuttle] backup complete: snapshot=%s bytes=%d", snapshotID, size)))
	return BackupOutcome{SnapshotID: snapshotID, SizeBytes: size}
}

// captureVolumes tars each of the project's named volumes into the stage dir as
// <key>.tar (uncompressed, so restic dedups effectively across snapshots).
func (d *ComposeDriver) captureVolumes(ctx context.Context, p BackupParams, composePath, envFile, stage string, emit func(LogLine)) error {
	vols, err := d.projectVolumes(ctx, composePath, envFile, p.Volumes)
	if err != nil {
		return fmt.Errorf("resolve volumes: %w", err)
	}
	if len(vols) == 0 {
		return fmt.Errorf("service %q has no named volumes to back up", p.Service)
	}
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	for _, v := range vols {
		emit(infoLine("[shuttle] backing up volume " + v.Name + " (" + v.Key + ")"))
		args := []string{
			"run", "--rm",
			"-v", v.Name + ":" + mountVol + ":ro",
			"-v", stage + ":" + mountStage,
		}
		args = append(args, envFlags...)
		args = append(args, tarImage, "tar", "cf", mountStage+"/"+sanitizeFile(v.Key)+".tar", "-C", mountVol, ".")
		if err := d.run(ctx, emit, nil, nil, cmdEnv, d.bin, args...); err != nil {
			return fmt.Errorf("tar volume %s: %w", v.Name, err)
		}
	}
	return nil
}

// capturePostgres runs pg_dump (or pg_dumpall) inside the DB container and writes
// the SQL to stage/dump.sql. The password travels through the container's env
// (-e PGPASSWORD passthrough), never the argument vector.
func (d *ComposeDriver) capturePostgres(ctx context.Context, p BackupParams, composePath, envFile, stage string, emit func(LogLine)) error {
	cid, err := d.serviceContainerID(ctx, composePath, envFile, p.DBService)
	if err != nil {
		return err
	}
	dumpPath := filepath.Join(stage, "dump.sql")
	f, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create dump file: %w", err)
	}
	defer func() { _ = f.Close() }()

	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	args := []string{"exec", "-i"}
	args = append(args, envFlags...)
	args = append(args, cid)
	args = append(args, pgDumpCmd(p.DBUser, p.DBName)...)
	emit(infoLine("[shuttle] pg dump of " + p.DBService))
	if err := d.run(ctx, emit, f, nil, cmdEnv, d.bin, args...); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	return nil
}

// resticBackup snapshots the stage directory into the restic repository. Returns
// the new snapshot's short id and the bytes restic processed.
func (d *ComposeDriver) resticBackup(ctx context.Context, p BackupParams, stage string, emit func(LogLine)) (string, int64, error) {
	if err := d.resticEnsureRepo(ctx, p.Target, p.Env, emit); err != nil {
		return "", 0, err
	}
	repoArg, repoMount := resticRepoLocation(p.Target)
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	args := []string{"run", "--rm", "-v", stage + ":" + mountStage + ":ro"}
	if repoMount != "" {
		args = append(args, "-v", repoMount+":"+mountRepo)
	}
	args = append(args, envFlags...)
	args = append(args, resticImage, "-r", repoArg, "backup", "--json",
		"--tag", "shuttle", "--tag", "service="+p.Service, mountStage)

	var stdout bytes.Buffer
	if err := d.run(ctx, emit, &stdout, nil, cmdEnv, d.bin, args...); err != nil {
		return "", 0, fmt.Errorf("restic backup: %w", err)
	}
	sid, nbytes, ok := parseResticSummary(stdout.Bytes())
	if !ok {
		return "", 0, fmt.Errorf("restic backup: could not parse snapshot id from output")
	}
	d.resticForget(ctx, p, repoArg, repoMount, emit)
	return sid, nbytes, nil
}

// resticForget applies the retention policy. Best-effort: a forget/prune failure
// is logged but does not fail the backup (the snapshot is already written).
func (d *ComposeDriver) resticForget(ctx context.Context, p BackupParams, repoArg, repoMount string, emit func(LogLine)) {
	keep := resticForgetArgs(p.Retention)
	if len(keep) == 0 {
		return
	}
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	args := []string{"run", "--rm"}
	if repoMount != "" {
		args = append(args, "-v", repoMount+":"+mountRepo)
	}
	args = append(args, envFlags...)
	args = append(args, resticImage, "-r", repoArg, "forget", "--prune", "--tag", "service="+p.Service)
	args = append(args, keep...)
	if err := d.run(ctx, emit, nil, nil, cmdEnv, d.bin, args...); err != nil {
		emit(infoLine("[shuttle] restic forget failed (snapshot kept): " + err.Error()))
	}
}

// resticEnsureRepo initializes the repository if it does not exist yet. `restic
// cat config` succeeds only on an initialized repo, so a failure triggers init.
func (d *ComposeDriver) resticEnsureRepo(ctx context.Context, target string, env map[string]string, emit func(LogLine)) error {
	repoArg, repoMount := resticRepoLocation(target)
	envFlags, cmdEnv := dockerEnvFlags(env)
	check := []string{"run", "--rm"}
	if repoMount != "" {
		if err := os.MkdirAll(repoMount, 0o700); err != nil {
			return fmt.Errorf("mkdir restic repo: %w", err)
		}
		check = append(check, "-v", repoMount+":"+mountRepo)
	}
	check = append(check, envFlags...)
	check = append(check, resticImage, "-r", repoArg, "cat", "config")
	if err := d.run(ctx, func(LogLine) {}, io.Discard, nil, cmdEnv, d.bin, check...); err == nil {
		return nil // repo already initialized
	}
	emit(infoLine("[shuttle] initializing restic repository"))
	initArgs := []string{"run", "--rm"}
	if repoMount != "" {
		initArgs = append(initArgs, "-v", repoMount+":"+mountRepo)
	}
	initArgs = append(initArgs, envFlags...)
	initArgs = append(initArgs, resticImage, "-r", repoArg, "init")
	if err := d.run(ctx, emit, nil, nil, cmdEnv, d.bin, initArgs...); err != nil {
		return fmt.Errorf("restic init: %w", err)
	}
	return nil
}

// runRestore stops the service, materializes the snapshot's artifacts into a
// stage dir, applies them, and starts the service again.
func (d *ComposeDriver) runRestore(ctx context.Context, p RestoreParams, emit func(LogLine)) BackupOutcome {
	composePath := filepath.Join(p.WorkDir, "docker-compose.yml")
	envFile := filepath.Join(p.WorkDir, ".env")
	if _, err := os.Stat(composePath); err != nil {
		return failOutcome(emit, fmt.Errorf("no compose workspace for %q: %w", p.Service, err))
	}

	// Materialize artifacts into a stage dir.
	var stage string
	if p.Store == backupStoreLocal {
		stage = filepath.Join(p.Target, p.Service, p.SnapshotID)
		if _, err := os.Stat(stage); err != nil {
			return failOutcome(emit, fmt.Errorf("local backup %q not found: %w", p.SnapshotID, err))
		}
	} else {
		tmp, err := os.MkdirTemp("", "shuttle-restore-")
		if err != nil {
			return failOutcome(emit, fmt.Errorf("stage dir: %w", err))
		}
		stage = tmp
		defer func() { _ = os.RemoveAll(tmp) }()
		if err := d.resticRestoreInto(ctx, p, stage, emit); err != nil {
			return failOutcome(emit, err)
		}
	}

	// Stop the service before touching its data; start it again afterward.
	emit(infoLine("[shuttle] stopping service for restore"))
	if err := d.run(ctx, emit, nil, nil, nil, d.bin, d.composeArgs(composePath, envFile, "stop")...); err != nil {
		return failOutcome(emit, fmt.Errorf("stop service: %w", err))
	}

	var applyErr error
	switch p.Engine {
	case backupEngineVolume:
		applyErr = d.restoreVolumes(ctx, p, composePath, envFile, stage, emit)
	case backupEnginePostgres:
		applyErr = d.restorePostgres(ctx, p, composePath, envFile, stage, emit)
	default:
		applyErr = fmt.Errorf("unknown backup engine %q", p.Engine)
	}

	emit(infoLine("[shuttle] starting service after restore"))
	if err := d.run(ctx, emit, nil, nil, nil, d.bin, d.composeArgs(composePath, envFile, "start")...); err != nil && applyErr == nil {
		applyErr = fmt.Errorf("start service: %w", err)
	}
	if applyErr != nil {
		return failOutcome(emit, applyErr)
	}
	emit(infoLine("[shuttle] restore complete"))
	return BackupOutcome{SnapshotID: p.SnapshotID}
}

// resticRestoreInto restores the snapshot's files into stage. restic recreates
// the snapshot's internal path (/stage/<artifact>) under the mounted stage, so
// after restore the artifacts sit directly in stage.
func (d *ComposeDriver) resticRestoreInto(ctx context.Context, p RestoreParams, stage string, emit func(LogLine)) error {
	repoArg, repoMount := resticRepoLocation(p.Target)
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	args := []string{"run", "--rm", "-v", stage + ":" + mountStage}
	if repoMount != "" {
		args = append(args, "-v", repoMount+":"+mountRepo)
	}
	args = append(args, envFlags...)
	args = append(args, resticImage, "-r", repoArg, "restore", p.SnapshotID, "--target", "/")
	if err := d.run(ctx, emit, nil, nil, cmdEnv, d.bin, args...); err != nil {
		return fmt.Errorf("restic restore: %w", err)
	}
	return nil
}

// restoreVolumes extracts each <key>.tar in stage back into its docker volume,
// clearing the volume first.
func (d *ComposeDriver) restoreVolumes(ctx context.Context, p RestoreParams, composePath, envFile, stage string, emit func(LogLine)) error {
	vols, err := d.projectVolumes(ctx, composePath, envFile, nil)
	if err != nil {
		return fmt.Errorf("resolve volumes: %w", err)
	}
	byKey := make(map[string]projectVolume, len(vols))
	for _, v := range vols {
		byKey[v.Key] = v
	}
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	entries, err := os.ReadDir(stage)
	if err != nil {
		return fmt.Errorf("read stage: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".tar") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".tar")
		v, ok := byKey[key]
		if !ok {
			emit(infoLine("[shuttle] skipping " + e.Name() + ": no matching volume in project"))
			continue
		}
		emit(infoLine("[shuttle] restoring volume " + v.Name))
		args := []string{
			"run", "--rm",
			"-v", v.Name + ":" + mountVol,
			"-v", stage + ":" + mountStage + ":ro",
		}
		args = append(args, envFlags...)
		args = append(args, tarImage, "sh", "-c",
			"find "+mountVol+" -mindepth 1 -delete && tar xf "+mountStage+"/"+sanitizeFile(key)+".tar -C "+mountVol)
		if err := d.run(ctx, emit, nil, nil, cmdEnv, d.bin, args...); err != nil {
			return fmt.Errorf("restore volume %s: %w", v.Name, err)
		}
	}
	return nil
}

// restorePostgres replays stage/dump.sql into the DB container via psql.
func (d *ComposeDriver) restorePostgres(ctx context.Context, p RestoreParams, composePath, envFile, stage string, emit func(LogLine)) error {
	cid, err := d.serviceContainerID(ctx, composePath, envFile, p.DBService)
	if err != nil {
		return err
	}
	dumpPath := filepath.Join(stage, "dump.sql")
	f, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("open dump: %w", err)
	}
	defer func() { _ = f.Close() }()
	envFlags, cmdEnv := dockerEnvFlags(p.Env)
	args := []string{"exec", "-i"}
	args = append(args, envFlags...)
	args = append(args, cid)
	args = append(args, pgRestoreCmd(p.DBUser, p.DBName)...)
	emit(infoLine("[shuttle] psql restore into " + p.DBService))
	if err := d.run(ctx, emit, nil, f, cmdEnv, d.bin, args...); err != nil {
		return fmt.Errorf("psql restore: %w", err)
	}
	return nil
}

// projectVolume is a compose volume's short key and resolved docker volume name.
type projectVolume struct {
	Key  string
	Name string
}

// projectVolumes resolves the docker volume names of the compose project from
// `compose config --format json`. When filter is non-empty only those keys are
// returned. A volume without an explicit resolved name falls back to the compose
// default <project>_<key>.
func (d *ComposeDriver) projectVolumes(ctx context.Context, composePath, envFile string, filter []string) ([]projectVolume, error) {
	args := append([]string{}, d.compose...)
	args = append(args, "-f", composePath, "--env-file", envFile, "config", "--format", "json")
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = filepath.Dir(composePath)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("compose config: %w", err)
	}
	return parseProjectVolumes(out, filter)
}

// parseProjectVolumes extracts (key, name) volume pairs from `compose config
// --format json` output, honoring an optional key filter.
func parseProjectVolumes(jsonOut []byte, filter []string) ([]projectVolume, error) {
	var cfg struct {
		Name    string `json:"name"`
		Volumes map[string]struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(jsonOut, &cfg); err != nil {
		return nil, fmt.Errorf("decode compose config: %w", err)
	}
	want := make(map[string]bool, len(filter))
	for _, f := range filter {
		want[f] = true
	}
	var out []projectVolume
	for key, v := range cfg.Volumes {
		if len(want) > 0 && !want[key] {
			continue
		}
		name := v.Name
		if name == "" {
			name = cfg.Name + "_" + key
		}
		out = append(out, projectVolume{Key: key, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// serviceContainerID returns the running container id for a compose service.
func (d *ComposeDriver) serviceContainerID(ctx context.Context, composePath, envFile, service string) (string, error) {
	args := append([]string{}, d.compose...)
	args = append(args, "-f", composePath, "--env-file", envFile, "ps", "-q", service)
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = filepath.Dir(composePath)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("compose ps %s: %w", service, err)
	}
	cid := strings.TrimSpace(string(out))
	if cid == "" {
		return "", fmt.Errorf("service %q has no running container", service)
	}
	// ps -q can list several ids (scaled service); use the first.
	if i := strings.IndexByte(cid, '\n'); i >= 0 {
		cid = cid[:i]
	}
	return cid, nil
}

// run executes one command, streaming its stderr to emit line-by-line. stdout is
// written to the stdout writer when provided, otherwise streamed to emit. stdin,
// when non-nil, is fed to the command. env replaces the process environment when
// non-nil. It blocks until the command exits.
func (d *ComposeDriver) run(ctx context.Context, emit func(LogLine), stdout io.Writer, stdin io.Reader, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		defer func() { emitLines(emit, "stdout", outBuf.Bytes()) }()
	}
	runErr := cmd.Run()
	emitLines(emit, "stderr", errBuf.Bytes())
	return runErr
}

// emitLines splits raw output into log lines and emits each.
func emitLines(emit func(LogLine), stream string, raw []byte) {
	if len(raw) == 0 {
		return
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		emit(LogLine{TsUnixMs: time.Now().UnixMilli(), Stream: stream, Text: line})
	}
}

func infoLine(text string) LogLine {
	return LogLine{TsUnixMs: time.Now().UnixMilli(), Stream: "stdout", Text: text}
}

// failOutcome emits the error as a log line and returns a failed outcome.
func failOutcome(emit func(LogLine), err error) BackupOutcome {
	emit(LogLine{TsUnixMs: time.Now().UnixMilli(), Stream: "stderr", Text: "[shuttle] backup error: " + err.Error()})
	return BackupOutcome{Failed: true, Err: err.Error()}
}

// dockerEnvFlags turns a backend-cred map into `-e KEY` passthrough flags plus
// the matching process environment, so secrets reach the helper container's env
// without appearing in the argument vector. Keys are sorted for determinism.
func dockerEnvFlags(env map[string]string) (flags []string, cmdEnv []string) {
	if len(env) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmdEnv = os.Environ()
	for _, k := range keys {
		flags = append(flags, "-e", k)
		cmdEnv = append(cmdEnv, k+"="+env[k])
	}
	return flags, cmdEnv
}

// resticRepoLocation maps a backup target to restic's -r argument and, for a
// local repository, the host path to bind-mount at mountRepo. A target with a
// backend scheme (s3:, b2:, sftp:, rclone:, rest:) or an explicit "restic:"
// prefix is remote (no mount); a bare path or "local:" prefix is a local repo.
func resticRepoLocation(target string) (repoArg, hostMount string) {
	t := strings.TrimSpace(target)
	if rest, ok := strings.CutPrefix(t, "local:"); ok {
		return mountRepo, rest
	}
	if rest, ok := strings.CutPrefix(t, "restic:"); ok {
		t = rest
	}
	if i := strings.IndexByte(t, ':'); i > 1 { // scheme like s3:, b2:, sftp: (i>1 so a Windows drive letter isn't a scheme)
		return t, ""
	}
	if strings.HasPrefix(t, "/") {
		return mountRepo, t
	}
	return t, ""
}

// pgDumpCmd builds the dump command: pg_dumpall when no database is named
// (captures every database and global roles), otherwise pg_dump of that one db.
func pgDumpCmd(user, db string) []string {
	if db == "" {
		args := []string{"pg_dumpall"}
		if user != "" {
			args = append(args, "-U", user)
		}
		return args
	}
	args := []string{"pg_dump"}
	if user != "" {
		args = append(args, "-U", user)
	}
	return append(args, db)
}

// pgRestoreCmd builds the psql replay command matching pgDumpCmd's output.
func pgRestoreCmd(user, db string) []string {
	args := []string{"psql"}
	if user != "" {
		args = append(args, "-U", user)
	}
	if db != "" {
		args = append(args, "-d", db)
	}
	return args
}

// resticForgetArgs builds the --keep-* flags for `restic forget`. Returns an
// empty slice when no retention is set (forget is then skipped).
func resticForgetArgs(r BackupRetention) []string {
	var args []string
	add := func(flag string, n int) {
		if n > 0 {
			args = append(args, flag, strconv.Itoa(n))
		}
	}
	add("--keep-last", r.KeepLast)
	add("--keep-daily", r.KeepDaily)
	add("--keep-weekly", r.KeepWeekly)
	add("--keep-monthly", r.KeepMonthly)
	return args
}

// parseResticSummary extracts the new snapshot's short id and the bytes processed
// from `restic backup --json` output (one JSON object per line; the final
// "summary" object carries the result).
func parseResticSummary(jsonOut []byte) (snapshotID string, bytes int64, ok bool) {
	for raw := range strings.SplitSeq(string(jsonOut), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, "\"summary\"") {
			continue
		}
		var summary struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
			TotalBytes  int64  `json:"total_bytes_processed"`
		}
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			continue
		}
		if summary.MessageType == "summary" && summary.SnapshotID != "" {
			return summary.SnapshotID, summary.TotalBytes, true
		}
	}
	return "", 0, false
}

// sanitizeFile makes a compose volume key safe as a filename component.
func sanitizeFile(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// dirSize sums the sizes of regular files under dir.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

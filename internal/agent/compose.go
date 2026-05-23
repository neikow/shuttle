package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LogLine is a single line of output from a compose operation.
type LogLine struct {
	TsUnixMs int64
	Stream   string // "stdout" or "stderr"
	Text     string
}

// ApplyParams holds the inputs for a compose deploy.
type ApplyParams struct {
	Service     string
	ComposeYAML []byte
	Env         map[string]string
	WorkDir     string // directory where compose file is written
}

// RollbackParams holds inputs for a compose rollback (same as apply but prior SHA's compose).
type RollbackParams = ApplyParams

// Driver executes docker compose operations on behalf of the orchestrator.
type Driver interface {
	Apply(ctx context.Context, p ApplyParams) (<-chan LogLine, error)
	Rollback(ctx context.Context, p RollbackParams) (<-chan LogLine, error)
	// Status returns a coarse aggregate status ("running", "exited", ...) for the
	// service's compose project in workDir.
	Status(ctx context.Context, service, workDir string) (string, error)
}

// ComposeDriver shells out to the Docker Compose CLI. The executable and the
// compose subcommand prefix are configurable so the same driver serves a
// standard host (`docker compose`) and a Synology NAS, where the Docker CLI
// lives at an absolute path outside the agent's PATH.
type ComposeDriver struct {
	bin     string   // executable, e.g. "docker" or "/usr/local/bin/docker"
	compose []string // subcommand prefix before compose flags, e.g. ["compose"]
}

// NewComposeDriver returns a driver for a standard Docker host (`docker compose`).
func NewComposeDriver() *ComposeDriver {
	return &ComposeDriver{bin: "docker", compose: []string{"compose"}}
}

// NewSynologyDriver targets Synology DSM Container Manager (DSM 7.2+). DSM ships
// the Docker CLI with the compose plugin at /usr/local/bin/docker, which is
// usually absent from PATH when the agent runs under Task Scheduler, so the
// binary defaults to that absolute path. A non-empty bin overrides it.
func NewSynologyDriver(bin string) *ComposeDriver {
	if bin == "" {
		bin = "/usr/local/bin/docker"
	}
	return &ComposeDriver{bin: bin, compose: []string{"compose"}}
}

// NewDriver builds a Driver for the named target. dockerBin, when non-empty,
// overrides the Docker executable. Returns an error for unknown targets.
func NewDriver(name, dockerBin string) (Driver, error) {
	switch name {
	case "", "compose", "docker":
		d := NewComposeDriver()
		if dockerBin != "" {
			d.bin = dockerBin
		}
		return d, nil
	case "synology":
		return NewSynologyDriver(dockerBin), nil
	default:
		return nil, fmt.Errorf("unknown driver %q (want compose|synology)", name)
	}
}

// composeArgs builds the full argument vector (excluding the executable) for a
// compose invocation against composePath with the given subcommand.
func (d *ComposeDriver) composeArgs(composePath, envFile string, sub ...string) []string {
	args := append([]string{}, d.compose...)
	args = append(args, "-f", composePath, "--env-file", envFile)
	return append(args, sub...)
}

func (d *ComposeDriver) Apply(ctx context.Context, p ApplyParams) (<-chan LogLine, error) {
	return d.runCompose(ctx, p, []string{"up", "-d", "--remove-orphans", "--pull", "always"})
}

func (d *ComposeDriver) Rollback(ctx context.Context, p RollbackParams) (<-chan LogLine, error) {
	return d.runCompose(ctx, p, []string{"up", "-d", "--remove-orphans"})
}

func (d *ComposeDriver) runCompose(ctx context.Context, p ApplyParams, subCmd []string) (<-chan LogLine, error) {
	// Resolve to an absolute path: cmd.Dir is set to the workdir, so a relative
	// -f / --env-file would be re-resolved against it and double the path.
	workDir, err := filepath.Abs(p.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdirall workdir: %w", err)
	}

	composePath := filepath.Join(workDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, p.ComposeYAML, 0600); err != nil {
		return nil, fmt.Errorf("write compose: %w", err)
	}

	envFile := filepath.Join(workDir, ".env")
	if err := writeEnvFile(envFile, p.Env); err != nil {
		return nil, fmt.Errorf("write env: %w", err)
	}

	cmd := exec.CommandContext(ctx, d.bin, d.composeArgs(composePath, envFile, subCmd...)...)
	cmd.Dir = workDir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("docker compose start: %w", err)
	}

	lines := make(chan LogLine, 64)

	go func() {
		defer close(lines)
		done := make(chan struct{}, 2)

		drain := func(scanner *bufio.Scanner, stream string) {
			for scanner.Scan() {
				lines <- LogLine{
					TsUnixMs: time.Now().UnixMilli(),
					Stream:   stream,
					Text:     scanner.Text(),
				}
			}
			done <- struct{}{}
		}

		go drain(bufio.NewScanner(stdoutPipe), "stdout")
		go drain(bufio.NewScanner(stderrPipe), "stderr")

		<-done
		<-done
		if err := cmd.Wait(); err != nil {
			lines <- LogLine{
				TsUnixMs: time.Now().UnixMilli(),
				Stream:   "stderr",
				Text:     fmt.Sprintf("[shuttle] compose error: %v", err),
			}
		}
	}()

	return lines, nil
}

// Status runs `docker compose ps -a` and returns an aggregate status for the
// project: "running" if any container is up, otherwise the first reported
// state (e.g. "exited" for a crashed container), or "stopped" when nothing is
// listed. The -a flag includes stopped containers so the drift reconciler can
// see a crash rather than an empty list.
func (d *ComposeDriver) Status(ctx context.Context, service, workDir string) (string, error) {
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	composePath := filepath.Join(workDir, "docker-compose.yml")
	args := append([]string{}, d.compose...)
	args = append(args, "-f", composePath, "ps", "-a", "--format", "{{.State}}")
	cmd := exec.CommandContext(ctx, d.bin, args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("docker compose ps: %w: %s", err, msg)
		}
		return "", fmt.Errorf("docker compose ps: %w", err)
	}
	states := strings.Fields(strings.TrimSpace(string(out)))
	if len(states) == 0 {
		return "stopped", nil
	}
	for _, s := range states {
		if strings.EqualFold(s, "running") {
			return "running", nil
		}
	}
	return states[0], nil
}

func writeEnvFile(path string, env map[string]string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for k, v := range env {
		// Escape newlines in values.
		escaped := strings.ReplaceAll(v, "\n", "\\n")
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, escaped); err != nil {
			return err
		}
	}
	return nil
}

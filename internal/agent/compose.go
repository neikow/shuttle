package agent

import (
	"bufio"
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
}

// ComposeDriver shells out to `docker compose`.
type ComposeDriver struct{}

func NewComposeDriver() *ComposeDriver { return &ComposeDriver{} }

func (d *ComposeDriver) Apply(ctx context.Context, p ApplyParams) (<-chan LogLine, error) {
	return d.runCompose(ctx, p, []string{"up", "-d", "--remove-orphans", "--pull", "always"})
}

func (d *ComposeDriver) Rollback(ctx context.Context, p RollbackParams) (<-chan LogLine, error) {
	return d.runCompose(ctx, p, []string{"up", "-d", "--remove-orphans"})
}

func (d *ComposeDriver) runCompose(ctx context.Context, p ApplyParams, subCmd []string) (<-chan LogLine, error) {
	if err := os.MkdirAll(p.WorkDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdirall workdir: %w", err)
	}

	composePath := filepath.Join(p.WorkDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, p.ComposeYAML, 0600); err != nil {
		return nil, fmt.Errorf("write compose: %w", err)
	}

	envFile := filepath.Join(p.WorkDir, ".env")
	if err := writeEnvFile(envFile, p.Env); err != nil {
		return nil, fmt.Errorf("write env: %w", err)
	}

	args := append([]string{"compose", "-f", composePath, "--env-file", envFile}, subCmd...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = p.WorkDir

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

func writeEnvFile(path string, env map[string]string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	for k, v := range env {
		// Escape newlines in values.
		escaped := strings.ReplaceAll(v, "\n", "\\n")
		fmt.Fprintf(f, "%s=%s\n", k, escaped)
	}
	return nil
}

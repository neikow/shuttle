package agent

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// rollingApply performs a zero-downtime update of the service's compose project:
// pull the new images, start new containers alongside the old ones (compose
// --scale + --no-recreate), let the caller join them to the ingress network,
// wait for them to become healthy, then remove the old containers. Any failure
// before the old containers are removed aborts and leaves the old version
// running — so a bad deploy never takes the service down.
//
// It works only when the project's containers can run two-up: no fixed published
// host port and no container_name. Those configs make the scale-up step fail,
// which the abort path handles (old version stays up, deploy reported FAILED).
// `shuttle check` warns about them ahead of time.
func (d *ComposeDriver) rollingApply(ctx context.Context, p ApplyParams) (<-chan LogLine, error) {
	workDir, composePath, envFile, err := d.prepareWorkspace(p)
	if err != nil {
		return nil, err
	}
	timeout := p.HealthTimeout
	if timeout <= 0 {
		timeout = defaultHealthTimeout
	}

	lines := make(chan LogLine, 64)
	go func() {
		defer close(lines)

		emit(lines, "[shuttle] rolling update: pulling images")
		if err := d.streamInto(ctx, lines, workDir, d.composeArgs(composePath, envFile, "pull")...); err != nil {
			emitErr(lines, "pull failed: %v", err)
			return
		}

		services, err := d.composeServices(ctx, workDir, composePath, envFile)
		if err != nil {
			emitErr(lines, "list compose services failed: %v", err)
			return
		}

		// Capture the containers running before the scale-up: these are the old
		// version, to be removed once the new ones are healthy.
		oldByService := make(map[string][]string, len(services))
		var oldAll []string
		for _, s := range services {
			ids, err := d.containerIDs(ctx, workDir, composePath, envFile, s)
			if err != nil {
				emitErr(lines, "list containers for %q failed: %v", s, err)
				return
			}
			oldByService[s] = ids
			oldAll = append(oldAll, ids...)
		}

		// Scale each service to twice its current count (at least 1), keeping the
		// old containers (--no-recreate) and adding new ones with the new config.
		scaleArgs := []string{"up", "-d", "--no-deps", "--no-recreate"}
		for _, s := range services {
			scaleArgs = append(scaleArgs, "--scale", fmt.Sprintf("%s=%d", s, targetScale(len(oldByService[s]))))
		}
		emit(lines, "[shuttle] rolling update: starting new containers")
		if err := d.streamInto(ctx, lines, workDir, d.composeArgs(composePath, envFile, scaleArgs...)...); err != nil {
			d.removeContainers(ctx, lines, d.newContainers(ctx, workDir, composePath, envFile, services, oldByService))
			emitErr(lines, "scale up failed (old version left running): %v", err)
			return
		}

		newAll := d.newContainers(ctx, workDir, composePath, envFile, services, oldByService)

		// Join the new containers to the ingress network before removing the old
		// ones, so traffic is served throughout.
		if p.OnNewContainers != nil && len(newAll) > 0 {
			if err := p.OnNewContainers(ctx, newAll); err != nil {
				emit(lines, fmt.Sprintf("[shuttle] warning: connect new containers to ingress: %v", err))
			}
		}

		emit(lines, "[shuttle] rolling update: waiting for new containers to become healthy")
		if err := d.waitHealthy(ctx, newAll, timeout); err != nil {
			d.removeContainers(ctx, lines, newAll)
			emitErr(lines, "new containers not healthy, rolled back (old version left running): %v", err)
			return
		}

		if len(oldAll) > 0 {
			emit(lines, "[shuttle] rolling update: removing old containers")
			d.removeContainers(ctx, lines, oldAll)
		}

		// Settle compose's replica count back to the desired number. The old
		// containers are already gone, so this only reconciles bookkeeping; a
		// failure here is non-fatal (the new version is already serving).
		settleArgs := []string{"up", "-d", "--no-deps", "--no-recreate"}
		for _, s := range services {
			n := len(oldByService[s])
			if n == 0 {
				n = 1
			}
			settleArgs = append(settleArgs, "--scale", fmt.Sprintf("%s=%d", s, n))
		}
		if err := d.streamInto(ctx, lines, workDir, d.composeArgs(composePath, envFile, settleArgs...)...); err != nil {
			emit(lines, fmt.Sprintf("[shuttle] warning: settle replica count: %v", err))
		}
		emit(lines, "[shuttle] rolling update complete")
	}()
	return lines, nil
}

// targetScale returns the transient replica count during a rolling update: twice
// the current count, or 1 when nothing is running yet (first deploy).
func targetScale(current int) int {
	if current == 0 {
		return 1
	}
	return current * 2
}

// newContainers returns the containers that exist now but did not before the
// scale-up (per service), i.e. the freshly created ones.
func (d *ComposeDriver) newContainers(ctx context.Context, workDir, composePath, envFile string, services []string, oldByService map[string][]string) []string {
	var out []string
	for _, s := range services {
		cur, err := d.containerIDs(ctx, workDir, composePath, envFile, s)
		if err != nil {
			continue
		}
		out = append(out, diffIDs(cur, oldByService[s])...)
	}
	return out
}

// composeServices lists the service names defined in the compose project.
func (d *ComposeDriver) composeServices(ctx context.Context, workDir, composePath, envFile string) ([]string, error) {
	out, err := d.output(ctx, workDir, d.composeArgs(composePath, envFile, "config", "--services")...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// containerIDs returns the container IDs of a single compose service.
func (d *ComposeDriver) containerIDs(ctx context.Context, workDir, composePath, envFile, service string) ([]string, error) {
	out, err := d.output(ctx, workDir, d.composeArgs(composePath, envFile, "ps", "-q", service)...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// waitHealthy blocks until every container is ready or the timeout elapses. A
// container with a healthcheck is ready when it reports "healthy" (and fails
// fast on "unhealthy"); one without a healthcheck is ready when it is running,
// after a short grace period.
func (d *ComposeDriver) waitHealthy(ctx context.Context, ids []string, timeout time.Duration) error {
	if len(ids) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	// Grace for containers with no healthcheck: give them time to bind before
	// "running" counts as ready.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(noHealthGrace):
	}
	for {
		allReady := true
		var pending []string
		for _, id := range ids {
			ready, err := d.containerReady(ctx, id)
			if err != nil {
				return err
			}
			if !ready {
				allReady = false
				pending = append(pending, shortID(id))
			}
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for: %s", timeout, strings.Join(pending, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// containerReady reports whether a single container is ready, returning an error
// when it has reported "unhealthy" (so the caller can fail fast).
func (d *ComposeDriver) containerReady(ctx context.Context, id string) (bool, error) {
	out, err := d.output(ctx, "", "inspect", "-f",
		"{{.State.Status}};{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", id)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", shortID(id), err)
	}
	status, health, _ := strings.Cut(strings.TrimSpace(out), ";")
	switch health {
	case "healthy":
		return true, nil
	case "unhealthy":
		return false, fmt.Errorf("container %s is unhealthy", shortID(id))
	case "none":
		return status == "running", nil
	default: // "starting" or anything transient
		return false, nil
	}
}

// removeContainers force-removes the given containers. Best-effort: the output
// is streamed and a failure is reported as a warning, not a hard error, so a
// stuck old container does not strand the new (already serving) version.
func (d *ComposeDriver) removeContainers(ctx context.Context, lines chan<- LogLine, ids []string) {
	if len(ids) == 0 {
		return
	}
	args := append([]string{"rm", "-f"}, ids...)
	if err := d.streamInto(ctx, lines, "", args...); err != nil {
		emit(lines, fmt.Sprintf("[shuttle] warning: remove containers: %v", err))
	}
}

// output runs the docker binary with args and returns trimmed stdout.
func (d *ComposeDriver) output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// streamInto runs the docker binary with args and streams its stdout/stderr
// line-by-line into lines (which it does not close), returning the command's
// exit error.
func (d *ComposeDriver) streamInto(ctx context.Context, lines chan<- LogLine, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	drain := func(scanner *bufio.Scanner, stream string) {
		defer wg.Done()
		for scanner.Scan() {
			lines <- LogLine{TsUnixMs: time.Now().UnixMilli(), Stream: stream, Text: scanner.Text()}
		}
	}
	wg.Add(2)
	go drain(bufio.NewScanner(stdout), "stdout")
	go drain(bufio.NewScanner(stderr), "stderr")
	wg.Wait()
	return cmd.Wait()
}

// emit writes a synthetic informational line to the stream.
func emit(lines chan<- LogLine, text string) {
	lines <- LogLine{TsUnixMs: time.Now().UnixMilli(), Stream: "stdout", Text: text}
}

// emitErr writes a synthetic error line. It carries the "[shuttle] compose
// error" marker so streamDeployResult flags the deploy as FAILED.
func emitErr(lines chan<- LogLine, format string, args ...any) {
	lines <- LogLine{
		TsUnixMs: time.Now().UnixMilli(),
		Stream:   "stderr",
		Text:     "[shuttle] compose error: " + fmt.Sprintf(format, args...),
	}
}

// diffIDs returns the elements of cur not present in old.
func diffIDs(cur, old []string) []string {
	set := make(map[string]struct{}, len(old))
	for _, id := range old {
		set[id] = struct{}{}
	}
	var out []string
	for _, id := range cur {
		if _, ok := set[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// shortID truncates a container ID for log readability.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

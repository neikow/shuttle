package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argvStubDriver writes a docker stub that appends its arguments to argv.log in
// workDir, so a test can assert which compose subcommand/flags were invoked.
func argvStubDriver(t *testing.T, workDir string) *ComposeDriver {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "fakedocker")
	script := "#!/bin/sh\necho \"$@\" >> \"" + filepath.Join(workDir, "argv.log") + "\"\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ComposeDriver{bin: stub, compose: []string{"compose"}}
}

func drain(t *testing.T, ch <-chan LogLine) {
	t.Helper()
	for line := range ch {
		if line.Stream == "stderr" && containsError(line.Text) {
			t.Fatalf("unexpected compose error: %s", line.Text)
		}
	}
}

func TestDown_missingWorkspaceIsNoop(t *testing.T) {
	dir := t.TempDir() // no docker-compose.yml inside
	d := argvStubDriver(t, dir)

	ch, err := d.Down(context.Background(), "svc", dir, false)
	if err != nil {
		t.Fatalf("Down on missing workspace: %v", err)
	}
	drain(t, ch)
	if _, err := os.Stat(filepath.Join(dir, "argv.log")); err == nil {
		t.Error("docker should not run when no compose file exists")
	}
}

func TestDown_invokesComposeDown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := argvStubDriver(t, dir)

	// Without volumes.
	ch, err := d.Down(context.Background(), "svc", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch)

	// With volumes.
	ch, err = d.Down(context.Background(), "svc", dir, true)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch)

	data, err := os.ReadFile(filepath.Join(dir, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 docker invocations, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "down") || !strings.Contains(lines[0], "--remove-orphans") {
		t.Errorf("first invocation = %q, want compose down --remove-orphans", lines[0])
	}
	if strings.Contains(lines[0], "--volumes") {
		t.Errorf("removeVolumes=false should not pass --volumes: %q", lines[0])
	}
	if !strings.Contains(lines[1], "--volumes") {
		t.Errorf("removeVolumes=true should pass --volumes: %q", lines[1])
	}
}

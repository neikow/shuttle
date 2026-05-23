package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// stubDriver writes an executable shell stub as the "docker" binary so Status
// can be exercised without a real Docker daemon. The stub emits the given
// stdout/stderr and exit code, ignoring its arguments.
func stubDriver(t *testing.T, stdout, stderr string, exit int) (*ComposeDriver, string) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "fakedocker")
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s' '" + stdout + "'\n"
	}
	if stderr != "" {
		script += "printf '%s' '" + stderr + "' >&2\n"
	}
	script += "exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ComposeDriver{bin: stub}, dir
}

func TestStatus_includesStderr(t *testing.T) {
	d, dir := stubDriver(t, "", "no configuration file provided", 1)
	_, err := d.Status(context.Background(), "svc", dir)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "no configuration file provided") {
		t.Errorf("error %q should include stderr", err)
	}
}

func TestStatus_states(t *testing.T) {
	tests := []struct {
		name, stdout, want string
	}{
		{"empty -> stopped", "", "stopped"},
		{"any running", "exited\nrunning\n", "running"},
		{"first non-running", "exited\nexited\n", "exited"},
	}
	for _, tt := range tests {
		d, dir := stubDriver(t, tt.stdout, "", 0)
		got, err := d.Status(context.Background(), "svc", dir)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("%s: Status = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNewDriver(t *testing.T) {
	tests := []struct {
		name      string
		dockerBin string
		wantBin   string
		wantErr   bool
	}{
		{name: "compose", wantBin: "docker"},
		{name: "", wantBin: "docker"},       // default
		{name: "docker", wantBin: "docker"}, // alias
		{name: "compose", dockerBin: "/custom/docker", wantBin: "/custom/docker"},
		{name: "synology", wantBin: "/usr/local/bin/docker"}, // DSM default path
		{name: "synology", dockerBin: "/opt/bin/docker", wantBin: "/opt/bin/docker"},
		{name: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		d, err := NewDriver(tt.name, tt.dockerBin)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NewDriver(%q,%q): expected error, got nil", tt.name, tt.dockerBin)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NewDriver(%q,%q): %v", tt.name, tt.dockerBin, err)
		}
		cd, ok := d.(*ComposeDriver)
		if !ok {
			t.Fatalf("NewDriver(%q): want *ComposeDriver, got %T", tt.name, d)
		}
		if cd.bin != tt.wantBin {
			t.Errorf("NewDriver(%q,%q): bin = %q, want %q", tt.name, tt.dockerBin, cd.bin, tt.wantBin)
		}
		if !reflect.DeepEqual(cd.compose, []string{"compose"}) {
			t.Errorf("NewDriver(%q): compose prefix = %v, want [compose]", tt.name, cd.compose)
		}
	}
}

func TestComposeArgs(t *testing.T) {
	d := NewComposeDriver()
	got := d.composeArgs("/w/docker-compose.yml", "/w/.env", "up", "-d")
	want := []string{"compose", "-f", "/w/docker-compose.yml", "--env-file", "/w/.env", "up", "-d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeArgs = %v, want %v", got, want)
	}
}

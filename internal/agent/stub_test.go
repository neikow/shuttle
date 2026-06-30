package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// scriptDriver writes an executable shell script as the docker binary and returns
// a ComposeDriver wired to it. The script body is a POSIX-sh program that may
// branch on "$*" (the full argument string) to emit per-subcommand output. A
// $CNT variable is pre-set to a unique counter file so a script can return
// different output across repeated invocations (e.g. `ps -q` before vs after a
// scale-up).
func scriptDriver(t *testing.T, body string) (*ComposeDriver, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakedocker")
	cnt := filepath.Join(dir, "calls.cnt")
	script := "#!/bin/sh\nCNT=\"" + cnt + "\"\nargs=\"$*\"\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ComposeDriver{bin: bin, compose: []string{"compose"}}, dir
}

// stubBin writes an executable shell script (same $args/$CNT contract as
// scriptDriver) and returns its path, for wiring as a sidecar's DockerBin.
func stubBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakedocker")
	cnt := filepath.Join(dir, "calls.cnt")
	script := "#!/bin/sh\nCNT=\"" + cnt + "\"\nargs=\"$*\"\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// drainText collects every line's text from a compose log channel.
func drainText(ch <-chan LogLine) []string {
	var out []string
	for l := range ch {
		out = append(out, l.Text)
	}
	return out
}

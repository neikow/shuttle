package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withStdin replaces os.Stdin with a pipe preloaded with input for the duration
// of the test, so the interactive wizards can be driven non-interactively.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(input)
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

func TestInitCmd_RunE(t *testing.T) {
	dir := t.TempDir()
	withStdin(t, "1\n") // repo mode: starter (ci pre-answered by --ci flag)
	if err := initCmd.RunE(flagCmd(map[string]string{"dir": dir, "ci": "none"}), nil); err != nil {
		t.Fatalf("init RunE: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts.yaml")); err != nil {
		t.Errorf("init should scaffold hosts.yaml: %v", err)
	}
}

func TestOrchestratorInitCmd_RunE(t *testing.T) {
	dir := t.TempDir()
	// transport choice (1=token) + secrets provider (1=none).
	withStdin(t, "1\n1\n")
	if err := orchestratorInitCmd.RunE(flagCmd(map[string]string{"dir": dir, "repo-url": ""}), nil); err != nil {
		t.Fatalf("orchestrator init RunE: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Errorf("orchestrator init should write config.yml: %v", err)
	}
}

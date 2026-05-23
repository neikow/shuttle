package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment line
export FOO=bar
QUOTED="hello world"
SINGLE='single quoted'
TRAILING=value # inline comment
SPACED = spaced
EMPTY=

PRESET=from-file
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PRESET", "from-env") // real env must win

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	want := map[string]string{
		"FOO":      "bar",
		"QUOTED":   "hello world",
		"SINGLE":   "single quoted",
		"TRAILING": "value",
		"SPACED":   "spaced",
		"EMPTY":    "",
		"PRESET":   "from-env",
	}
	for k, v := range want {
		t.Setenv(k, os.Getenv(k)) // ensure cleanup of vars LoadDotEnv set
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestLoadDotEnv_missingFileOK(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Errorf("missing file should be nil, got %v", err)
	}
}

func TestLoadDotEnv_malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("=novalue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(path); err == nil {
		t.Error("empty key should error")
	}
}

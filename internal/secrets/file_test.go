package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, root, env, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, env, relPath+".env")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileProvider_GetAll(t *testing.T) {
	root := t.TempDir()
	writeEnvFile(t, root, "prod", "services/web", `
# a comment
export QUOTED="hello world"
PLAIN=value
TRAILING=ok   # inline comment
BLANK=

bogus-line-without-eq
`)
	p := &FileProvider{root: root, defaultEnv: "production"}

	got, err := p.GetAll(context.Background(), Scope{Env: "prod", Path: "/services/web"})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	want := map[string]string{
		"QUOTED":   "hello world",
		"PLAIN":    "value",
		"TRAILING": "ok",
		"BLANK":    "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys %v, want %d %v", len(got), got, len(want), want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestFileProvider_Get(t *testing.T) {
	root := t.TempDir()
	writeEnvFile(t, root, "prod", "services/web", "API_KEY=secret123\n")
	p := &FileProvider{root: root, defaultEnv: "production"}

	v, err := p.Get(context.Background(), Scope{Env: "prod", Path: "/services/web"}, "API_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != "secret123" {
		t.Errorf("API_KEY = %q, want secret123", v)
	}

	_, err = p.Get(context.Background(), Scope{Env: "prod", Path: "/services/web"}, "MISSING")
	var nf ErrNotFound
	if !errors.As(err, &nf) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileProvider_MissingFileIsEmpty(t *testing.T) {
	p := &FileProvider{root: t.TempDir(), defaultEnv: "production"}
	got, err := p.GetAll(context.Background(), Scope{Env: "prod", Path: "/nope"})
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should be empty, got %v", got)
	}
}

func TestFileProvider_fileFor(t *testing.T) {
	p := &FileProvider{root: "/secrets", defaultEnv: "production"}
	tests := []struct {
		scope Scope
		want  string
	}{
		{Scope{Env: "prod", Path: "/services/web"}, "/secrets/prod/services/web.env"},
		{Scope{Env: "prod", Path: "/shared"}, "/secrets/prod/shared.env"},
		{Scope{Path: "/shared"}, "/secrets/production/shared.env"}, // default env
		{Scope{Env: "prod", Path: "/../../etc/passwd"}, "/secrets/prod/etc/passwd.env"}, // traversal neutralized
	}
	for _, tt := range tests {
		if got := p.fileFor(tt.scope); got != tt.want {
			t.Errorf("fileFor(%+v) = %q, want %q", tt.scope, got, tt.want)
		}
	}
}

func TestNewProviderFile(t *testing.T) {
	t.Setenv("SHUTTLE_SECRETS_DIR", t.TempDir())
	t.Setenv("SHUTTLE_SECRETS_ENV", "staging")
	p, err := NewProvider("file")
	if err != nil {
		t.Fatalf("NewProvider(file): %v", err)
	}
	fp, ok := p.(*FileProvider)
	if !ok {
		t.Fatalf("expected *FileProvider, got %T", p)
	}
	if fp.defaultEnv != "staging" {
		t.Errorf("defaultEnv = %q, want staging", fp.defaultEnv)
	}
}

func TestNewProviderFile_RequiresDir(t *testing.T) {
	t.Setenv("SHUTTLE_SECRETS_DIR", "")
	if _, err := NewProvider("file"); err == nil {
		t.Error("expected error when SHUTTLE_SECRETS_DIR is unset")
	}
}

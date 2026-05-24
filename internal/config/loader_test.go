package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrchestratorConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	body := "bearer_token: s3cret\ngrpc_addr: \":9090\"\nhttp_addr: \":8080\"\ndata_dir: /var/lib/shuttle\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadOrchestratorConfig(path)
	if err != nil {
		t.Fatalf("LoadOrchestratorConfig: %v", err)
	}
	if cfg.BearerToken != "s3cret" || cfg.GRPCAddr != ":9090" || cfg.DataDir != "/var/lib/shuttle" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadOrchestratorConfig_missingToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("grpc_addr: \":9090\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrchestratorConfig(path); err == nil {
		t.Fatal("expected error for missing bearer_token")
	}
}

func TestLoad_fixture(t *testing.T) {
	root := filepath.Join("..", "..", "test", "fixtures", "repo")
	repo, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(repo.Hosts) != 2 {
		t.Errorf("want 2 hosts, got %d", len(repo.Hosts))
	}
	if len(repo.Services) != 1 {
		t.Errorf("want 1 service, got %d", len(repo.Services))
	}
	svc := repo.Services[0]
	if svc.Name != "app" {
		t.Errorf("want service name 'app', got %q", svc.Name)
	}
	lc, ok := svc.Source.(LocalCompose)
	if !ok {
		t.Fatalf("want LocalCompose source, got %T", svc.Source)
	}
	if lc.Path == "" {
		t.Error("compose path empty")
	}
}

func TestLoad_xorViolation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), "hosts:\n  - name: h1\n")
	svcDir := filepath.Join(dir, "services", "bad")
	os.MkdirAll(svcDir, 0755)
	writeFile(t, filepath.Join(svcDir, "bad.yaml"),
		"name: bad\nhost: h1\nremote:\n  repo: x\n  branch: main\n  path: .\n")
	writeFile(t, filepath.Join(svcDir, "docker-compose.yml"), "services: {}\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected XOR error, got nil")
	}
}

func TestLoad_unknownHost(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), "hosts:\n  - name: h1\n")
	svcDir := filepath.Join(dir, "services", "svc")
	os.MkdirAll(svcDir, 0755)
	writeFile(t, filepath.Join(svcDir, "svc.yaml"), "name: svc\nhost: nonexistent\n")
	writeFile(t, filepath.Join(svcDir, "docker-compose.yml"), "services: {}\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected unknown host error, got nil")
	}
}

func TestLoad_malformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), "hosts: [\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestLoad_unknownField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"),
		"hosts:\n  - name: h1\nunknown_field: oops\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected unknown field error, got nil")
	}
}

func TestLoadOrchestratorConfig_gitCredentials(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "valid credential",
			body: "bearer_token: tok\ngit_credentials:\n  - repo_prefix: github.com/myorg\n    infisical_key: GITHUB_TOKEN\n",
		},
		{
			name:    "empty repo_prefix",
			body:    "bearer_token: tok\ngit_credentials:\n  - repo_prefix: \"\"\n    infisical_key: GITHUB_TOKEN\n",
			wantErr: "repo_prefix is required",
		},
		{
			name:    "empty infisical_key",
			body:    "bearer_token: tok\ngit_credentials:\n  - repo_prefix: github.com/myorg\n    infisical_key: \"\"\n",
			wantErr: "infisical_key is required",
		},
		{
			name:    "repo_prefix with https:// scheme",
			body:    "bearer_token: tok\ngit_credentials:\n  - repo_prefix: https://github.com/myorg\n    infisical_key: GITHUB_TOKEN\n",
			wantErr: "repo_prefix must not include the scheme",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadOrchestratorConfig(path)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

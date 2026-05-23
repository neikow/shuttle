package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecretsPaths(t *testing.T) {
	tests := []struct {
		name                             string
		base, template, svcPath, svcName string
		wantBase, wantService            string
	}{
		{"explicit secret_path wins", "/shared", "/services/{service}", "/custom", "api", "/shared", "/custom"},
		{"template substituted", "/shared", "/services/{service}", "", "api", "/shared", "/services/api"},
		{"no template -> base", "/shared", "", "", "api", "/shared", "/shared"},
		{"empty base defaults", "", "", "", "api", "/shared", "/shared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, svc := ResolveSecretsPaths(tt.base, tt.template, tt.svcPath, tt.svcName)
			if base != tt.wantBase || svc != tt.wantService {
				t.Errorf("ResolveSecretsPaths = (%q, %q), want (%q, %q)", base, svc, tt.wantBase, tt.wantService)
			}
		})
	}
}

func TestLoadOrchestratorConfig_relativeSecretsPathRejected(t *testing.T) {
	for _, body := range []string{
		"bearer_token: t\nsecrets_base_path: shared\n",
		"bearer_token: t\nsecrets_path_template: services/{service}\n",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrchestratorConfig(path); err == nil {
			t.Errorf("expected error for relative path in:\n%s", body)
		}
	}
}

func TestLoadService_relativeSecretPathRejected(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hosts.yaml", "hosts:\n  - name: web1\n")
	write("services/api/api.yaml", "name: api\nhost: web1\nsecret_path: relative/path\n")
	write("services/api/docker-compose.yml", "services: {}\n")

	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for relative secret_path")
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExternalRepo builds a minimal repo with one service dir holding the given
// service yaml (and optionally a compose file).
func writeExternalRepo(t *testing.T, svcYAML string, withCompose bool) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), "hosts:\n  - name: web1\n")
	svcDir := filepath.Join(dir, "services", "infisical")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(svcDir, "infisical.yaml"), svcYAML)
	if withCompose {
		writeFile(t, filepath.Join(svcDir, "docker-compose.yml"), "services: {}\n")
	}
	return dir
}

func TestLoad_externalService(t *testing.T) {
	dir := writeExternalRepo(t,
		"name: infisical\nhost: web1\ndomains: [infisical.example.com]\nexternal:\n  upstream: infisical:8080\n", false)
	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc := repo.Services[0]
	if !svc.IsExternal() {
		t.Fatalf("want external service, got %T", svc.Source)
	}
	ext, ok := svc.Source.(ExternalService)
	if !ok || ext.Upstream != "infisical:8080" {
		t.Fatalf("upstream = %+v, want infisical:8080", svc.Source)
	}
}

func TestLoad_externalService_invalid(t *testing.T) {
	tests := map[string]struct {
		yaml        string
		withCompose bool
	}{
		"missing upstream": {yaml: "name: infisical\nhost: web1\ndomains: [x.com]\nexternal: {}\n"},
		"missing domains":  {yaml: "name: infisical\nhost: web1\nexternal:\n  upstream: infisical:8080\n"},
		"xor with compose": {
			yaml:        "name: infisical\nhost: web1\ndomains: [x.com]\nexternal:\n  upstream: infisical:8080\n",
			withCompose: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := writeExternalRepo(t, tc.yaml, tc.withCompose)
			if _, err := Load(dir); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

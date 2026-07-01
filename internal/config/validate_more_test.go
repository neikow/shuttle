package config

import (
	"strings"
	"testing"
)

func problemMsgs(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Message)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestValidateBytes_OrchestratorOIDC(t *testing.T) {
	// issuer set but audience missing + empty role_mapping + bad enum.
	bad := []byte(`bearer_token: t
secrets_provider: bogus
oidc:
  issuer: https://idp
notifications:
  - type: pigeon
    url: x
`)
	got := problemMsgs(ValidateBytes(FileKindOrchestrator, bad))
	for _, want := range []string{"audience", "role_mapping", "bogus", "pigeon"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected a problem mentioning %q, got:\n%s", want, got)
		}
	}

	// Bad role value in role_mapping.
	badRole := []byte(`bearer_token: t
oidc:
  issuer: https://idp
  audience: aud
  role_mapping:
    admins: superuser
`)
	if got := problemMsgs(ValidateBytes(FileKindOrchestrator, badRole)); !strings.Contains(got, "superuser") {
		t.Errorf("expected invalid-role problem, got:\n%s", got)
	}

	// Valid OIDC -> no OIDC problems.
	ok := []byte(`bearer_token: t
oidc:
  issuer: https://idp
  audience: aud
  role_mapping:
    admins: admin
`)
	if got := problemMsgs(ValidateBytes(FileKindOrchestrator, ok)); strings.Contains(got, "role_mapping must not be empty") || strings.Contains(got, "audience") {
		t.Errorf("valid OIDC should have no OIDC problems, got:\n%s", got)
	}
}

func TestLoad_remoteAndExternalSources(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hosts.yaml", "hosts:\n  - name: web1\n")

	// Remote-pointer service.
	writeFile(t, dir+"/services/r/r.yaml", "name: r\nhost: web1\nremote:\n  repo: https://example.com/repo.git\n  path: compose.yml\n")
	// External (proxy-only) service.
	writeFile(t, dir+"/services/e/e.yaml", "name: e\nhost: web1\ndomains: [e.example.com]\nexternal:\n  upstream: http://up:8080\n")

	repo, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var sawExternal, sawRemote bool
	for _, s := range repo.Services {
		if s.Name == "e" && s.IsExternal() {
			sawExternal = true
		}
		if s.Name == "r" && !s.IsExternal() {
			sawRemote = true
		}
	}
	if !sawExternal || !sawRemote {
		t.Errorf("external=%v remote=%v, want both parsed", sawExternal, sawRemote)
	}
}

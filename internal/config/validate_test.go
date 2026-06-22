package config

import (
	"slices"
	"testing"
)

func TestDetectFileKind(t *testing.T) {
	tests := map[string]FileKind{
		"/repo/hosts.yaml":                      FileKindHosts,
		"/repo/dns.yml":                         FileKindDNS,
		"/repo/orchestrator.yaml":               FileKindRepoOrchestrator,
		"/srv/config.yml":                       FileKindOrchestrator,
		"/repo/services/api/api.yaml":           FileKindService,
		"/repo/services/web/web.yaml":           FileKindService,
		"/repo/services/api/docker-compose.yml": FileKindUnknown,
		"/repo/README.md":                       FileKindUnknown,
	}
	for path, want := range tests {
		if got := DetectFileKind(path); got != want {
			t.Errorf("DetectFileKind(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestValidateBytes_unknownKey(t *testing.T) {
	// "hostz" is not a field of serviceFile -> a positioned unknown-field problem.
	data := []byte("name: api\nhost: web1\nhostz: oops\n")
	probs := ValidateBytes(FileKindService, data)
	if len(probs) != 1 {
		t.Fatalf("want 1 problem, got %d: %+v", len(probs), probs)
	}
	if probs[0].Line != 3 {
		t.Errorf("line = %d, want 3", probs[0].Line)
	}
	if probs[0].Message == "" {
		t.Error("expected a non-empty message")
	}
}

func TestValidateBytes_validAndEmpty(t *testing.T) {
	if p := ValidateBytes(FileKindService, []byte("name: api\nhost: web1\ndomains: [x.com]\n")); len(p) != 0 {
		t.Errorf("valid service should have no problems, got %+v", p)
	}
	if p := ValidateBytes(FileKindDNS, []byte("# empty\n")); len(p) != 0 {
		t.Errorf("empty dns.yml should be valid, got %+v", p)
	}
	if p := ValidateBytes(FileKindUnknown, []byte("anything: goes\n")); p != nil {
		t.Errorf("unknown kind should yield nil, got %+v", p)
	}
}

func TestValidateBytes_syntaxError(t *testing.T) {
	probs := ValidateBytes(FileKindHosts, []byte("hosts: [\n"))
	if len(probs) == 0 {
		t.Fatal("expected a syntax problem")
	}
}

func TestFieldNamesAt(t *testing.T) {
	// Top-level service keys.
	top := FieldNamesAt(FileKindService, nil)
	for _, want := range []string{"name", "host", "domains", "external", "backup", "tls_certificate"} {
		if !slices.Contains(top, want) {
			t.Errorf("service top-level missing %q (got %v)", want, top)
		}
	}
	// Nested into the external block (a pointer to a struct).
	if got := FieldNamesAt(FileKindService, []string{"external"}); len(got) != 1 || got[0] != "upstream" {
		t.Errorf("external keys = %v, want [upstream]", got)
	}
	// Descend into a slice-of-struct (dns providers / certificates).
	prov := FieldNamesAt(FileKindDNS, []string{"providers"})
	for _, want := range []string{"name", "type", "endpoint", "credentials"} {
		if !slices.Contains(prov, want) {
			t.Errorf("dns provider keys missing %q (got %v)", want, prov)
		}
	}
	cert := FieldNamesAt(FileKindDNS, []string{"certificates"})
	for _, want := range []string{"name", "domains", "provider"} {
		if !slices.Contains(cert, want) {
			t.Errorf("dns certificate keys missing %q (got %v)", want, cert)
		}
	}
	// Unknown path -> nil.
	if got := FieldNamesAt(FileKindService, []string{"nope"}); got != nil {
		t.Errorf("unknown path should be nil, got %v", got)
	}

	// A map field (credentials) resolves to no struct fields at its own level —
	// its keys are provider-specific data, not SecretRef fields.
	if got := FieldNamesAt(FileKindDNS, []string{"providers", "credentials"}); got != nil {
		t.Errorf("credentials level should be nil (map keys are data), got %v", got)
	}
	// One level deeper (under a credential name) → the SecretRef fields.
	deep := FieldNamesAt(FileKindDNS, []string{"providers", "credentials", "application_key"})
	for _, want := range []string{"infisical_key", "infisical_env", "infisical_path"} {
		if !slices.Contains(deep, want) {
			t.Errorf("credential value keys missing %q (got %v)", want, deep)
		}
	}
}

func TestDNSProviderCredentialKeys(t *testing.T) {
	tests := map[string][]string{
		"ovh":        {"application_key", "application_secret", "consumer_key"},
		"cloudflare": {"api_token"},
		"route53":    {"access_key_id", "secret_access_key", "region"},
	}
	for typ, want := range tests {
		if got := DNSProviderCredentialKeys(typ); !slices.Equal(got, want) {
			t.Errorf("%s credential keys = %v, want %v", typ, got, want)
		}
	}
	if got := DNSProviderCredentialKeys("bogus"); got != nil {
		t.Errorf("unknown provider type should be nil, got %v", got)
	}
}

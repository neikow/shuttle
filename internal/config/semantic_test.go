package config

import (
	"slices"
	"strings"
	"testing"
)

// hasProblemContaining reports whether any problem's message contains sub, and
// the line it was reported on (0 if not found).
func hasProblemContaining(probs []Problem, sub string) (bool, int) {
	for _, p := range probs {
		if strings.Contains(p.Message, sub) {
			return true, p.Line
		}
	}
	return false, 0
}

func TestSemantic_serviceEnumAndRequired(t *testing.T) {
	// Bad update_policy enum, positioned on its line.
	probs := ValidateBytes(FileKindService, []byte("name: api\nhost: web1\nupdate_policy: bogus\n"))
	if ok, line := hasProblemContaining(probs, "update_policy"); !ok || line != 3 {
		t.Errorf("want update_policy enum problem on line 3, got %+v", probs)
	}

	// Missing required name + host.
	probs = ValidateBytes(FileKindService, []byte("port: 8080\n"))
	if ok, _ := hasProblemContaining(probs, "name is required"); !ok {
		t.Errorf("want 'name is required', got %+v", probs)
	}
	if ok, _ := hasProblemContaining(probs, "host is required"); !ok {
		t.Errorf("want 'host is required', got %+v", probs)
	}

	// A complete service has no semantic problems.
	if p := ValidateBytes(FileKindService, []byte("name: api\nhost: web1\nupdate_policy: rolling\n")); len(p) != 0 {
		t.Errorf("valid service should have no problems, got %+v", p)
	}
}

func TestSemantic_serviceBackupAndExternal(t *testing.T) {
	// backup.engine invalid + backup.store invalid.
	probs := ValidateBytes(FileKindService, []byte("name: api\nhost: web1\nbackup:\n  engine: nope\n  store: bad\n"))
	if ok, _ := hasProblemContaining(probs, "backup.engine"); !ok {
		t.Errorf("want backup.engine problem, got %+v", probs)
	}
	if ok, _ := hasProblemContaining(probs, "backup.store"); !ok {
		t.Errorf("want backup.store problem, got %+v", probs)
	}

	// postgres engine without db_service.
	probs = ValidateBytes(FileKindService, []byte("name: api\nhost: web1\nbackup:\n  engine: postgres\n"))
	if ok, _ := hasProblemContaining(probs, "db_service"); !ok {
		t.Errorf("want db_service required, got %+v", probs)
	}

	// external block without upstream / domains.
	probs = ValidateBytes(FileKindService, []byte("name: api\nhost: web1\nexternal:\n  {}\n"))
	if ok, _ := hasProblemContaining(probs, "external.upstream"); !ok {
		t.Errorf("want external.upstream required, got %+v", probs)
	}
	if ok, _ := hasProblemContaining(probs, "domain"); !ok {
		t.Errorf("want a domain-required problem, got %+v", probs)
	}
}

func TestSemantic_dnsProviderAndCertRef(t *testing.T) {
	// Unsupported provider type.
	probs := ValidateBytes(FileKindDNS, []byte("providers:\n  - name: x\n    type: bogus\n"))
	if ok, _ := hasProblemContaining(probs, "provider type"); !ok {
		t.Errorf("want provider type problem, got %+v", probs)
	}

	// Certificate referencing an undeclared provider (intra-file).
	probs = ValidateBytes(FileKindDNS, []byte("providers:\n  - name: ovh\n    type: ovh\ncertificates:\n  - name: star\n    domains: [\"*.x.com\"]\n    provider: nope\n"))
	if ok, _ := hasProblemContaining(probs, "unknown provider"); !ok {
		t.Errorf("want unknown-provider reference problem, got %+v", probs)
	}

	// Valid dns config: no semantic problems.
	if p := ValidateBytes(FileKindDNS, []byte("providers:\n  - name: ovh\n    type: ovh\ncertificates:\n  - name: star\n    domains: [\"*.x.com\"]\n    provider: ovh\n")); len(p) != 0 {
		t.Errorf("valid dns should have no problems, got %+v", p)
	}
}

func TestSemantic_orchestratorEnumsAndOIDC(t *testing.T) {
	probs := ValidateBytes(FileKindOrchestrator, []byte("bearer_token: t\nsecrets_provider: vault\n"))
	if ok, _ := hasProblemContaining(probs, "secrets_provider"); !ok {
		t.Errorf("want secrets_provider enum problem, got %+v", probs)
	}

	// Notification missing url + bad type.
	probs = ValidateBytes(FileKindOrchestrator, []byte("bearer_token: t\nnotifications:\n  - type: sms\n"))
	if ok, _ := hasProblemContaining(probs, "notification type"); !ok {
		t.Errorf("want notification type problem, got %+v", probs)
	}
	if ok, _ := hasProblemContaining(probs, "url is required"); !ok {
		t.Errorf("want url required, got %+v", probs)
	}

	// OIDC issuer set but no audience / role_mapping.
	probs = ValidateBytes(FileKindOrchestrator, []byte("bearer_token: t\noidc:\n  issuer: https://idp\n"))
	if ok, _ := hasProblemContaining(probs, "oidc.audience"); !ok {
		t.Errorf("want oidc.audience required, got %+v", probs)
	}
	if ok, _ := hasProblemContaining(probs, "role_mapping"); !ok {
		t.Errorf("want role_mapping required, got %+v", probs)
	}

	// Missing bearer_token.
	if ok, _ := hasProblemContaining(ValidateBytes(FileKindOrchestrator, []byte("data_dir: /x\n")), "bearer_token is required"); !ok {
		t.Error("want bearer_token required")
	}
}

func TestSemantic_hostsPortRange(t *testing.T) {
	probs := ValidateBytes(FileKindHosts, []byte("hosts:\n  - name: web1\n    caddy:\n      http_port: 99999\n"))
	if ok, _ := hasProblemContaining(probs, "http_port"); !ok {
		t.Errorf("want http_port range problem, got %+v", probs)
	}
	// Missing host name.
	if ok, _ := hasProblemContaining(ValidateBytes(FileKindHosts, []byte("hosts:\n  - labels: {}\n")), "name is required"); !ok {
		t.Error("want host name required")
	}
}

func TestFieldsAt_typesAndRequired(t *testing.T) {
	fields := FieldsAt(FileKindService, nil)
	byName := map[string]FieldInfo{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	if byName["name"].Type != "string" || !byName["name"].Required {
		t.Errorf("name should be a required string, got %+v", byName["name"])
	}
	if byName["host"].Type != "string" || !byName["host"].Required {
		t.Errorf("host should be a required string, got %+v", byName["host"])
	}
	if byName["port"].Type != "integer" {
		t.Errorf("port should be integer, got %q", byName["port"].Type)
	}
	if byName["domains"].Type != "list" {
		t.Errorf("domains should be list, got %q", byName["domains"].Type)
	}
	if byName["external"].Type != "object" {
		t.Errorf("external should be object, got %q", byName["external"].Type)
	}
	if byName["update_policy"].Required {
		t.Error("update_policy is not required")
	}
	// Sorted by name.
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	if !slices.IsSorted(names) {
		t.Errorf("fields not sorted: %v", names)
	}
}

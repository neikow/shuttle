package config

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// semanticProblems runs the schema checks that a strict YAML decode cannot catch
// — invalid enum values, missing required fields, and intra-file references
// (e.g. a dns certificate naming a provider declared in the same file). It walks
// the parsed node tree so each problem carries the offending node's position. It
// mirrors the loader/normalizers (and reuses their value sets via enums.go) so
// the editor stays in lockstep with what Shuttle accepts. Cross-file references
// (a service's host / tls_certificate) need sibling files and live in
// internal/lsp, not here, so this stays disk-free.
func semanticProblems(kind FileKind, root *yaml.Node) []Problem {
	m := rootMapping(root)
	if m == nil {
		return nil
	}
	var out []Problem
	switch kind {
	case FileKindService:
		out = semanticService(m)
	case FileKindHosts:
		out = semanticHosts(m)
	case FileKindDNS:
		out = semanticDNS(m)
	case FileKindOrchestrator:
		out = semanticOrchestrator(m)
	}
	return out
}

func semanticService(m *yaml.Node) []Problem {
	var out []Problem
	out = append(out, requiredHere(m, FileKindService, nil)...)
	out = appendEnum(out, m, "update_policy", UpdatePolicyValues)

	if b := mapMapping(m, "backup"); b != nil {
		out = append(out, requiredHere(b, FileKindService, []string{"backup"})...)
		out = appendEnumLabeled(out, b, "engine", BackupEngineValues, "backup.engine")
		out = appendEnumLabeled(out, b, "store", BackupStoreValues, "backup.store")
		if eng, _ := scalarOf(mapEntry(b, "engine")); eng == BackupEnginePostgres {
			out = appendRequiredLabeled(out, b, "db_service", `backup.db_service (required for the "postgres" engine)`)
		}
	}

	if e := mapMapping(m, "external"); e != nil {
		out = appendRequiredLabeled(out, e, "upstream", "external.upstream")
		// An external service is route-only, so it needs at least one domain.
		if !hasNonEmpty(m, "domains") {
			out = append(out, problemAt(m, "an external service needs at least one domain to route"))
		}
	}
	return out
}

func semanticHosts(m *yaml.Node) []Problem {
	var out []Problem
	for _, item := range seqItems(mapEntry(m, "hosts")) {
		if item.Kind != yaml.MappingNode {
			continue
		}
		out = append(out, requiredHere(item, FileKindHosts, []string{"hosts"})...)
		if c := mapMapping(item, "caddy"); c != nil {
			out = appendPort(out, c, "http_port")
			out = appendPort(out, c, "https_port")
		}
	}
	return out
}

func semanticDNS(m *yaml.Node) []Problem {
	var out []Problem
	providerNames := map[string]bool{}
	for _, item := range seqItems(mapEntry(m, "providers")) {
		if item.Kind != yaml.MappingNode {
			continue
		}
		out = append(out, requiredHere(item, FileKindDNS, []string{"providers"})...)
		out = appendEnumLabeled(out, item, "type", DNSProviderTypeNames(), "provider type")
		if name, ok := scalarOf(mapEntry(item, "name")); ok {
			providerNames[name] = true
		}
	}
	for _, item := range seqItems(mapEntry(m, "certificates")) {
		if item.Kind != yaml.MappingNode {
			continue
		}
		out = append(out, requiredHere(item, FileKindDNS, []string{"certificates"})...)
		// Intra-file reference: the certificate's provider must be declared above.
		if pv := mapEntry(item, "provider"); pv != nil {
			if name, ok := scalarOf(pv); ok && name != "" && !providerNames[name] {
				out = append(out, problemAt(pv, fmt.Sprintf("references unknown provider %q", name)))
			}
		}
	}
	return out
}

func semanticOrchestrator(m *yaml.Node) []Problem {
	var out []Problem
	out = append(out, requiredHere(m, FileKindOrchestrator, nil)...)
	out = appendEnum(out, m, "secrets_provider", SecretsProviderValues)

	if b := mapMapping(m, "backups"); b != nil {
		out = appendEnumLabeled(out, b, "default_store", BackupStoreValues, "backups.default_store")
		for _, item := range seqItems(mapEntry(b, "env")) {
			if item.Kind == yaml.MappingNode {
				out = append(out, requiredHere(item, FileKindOrchestrator, []string{"backups", "env"})...)
			}
		}
	}
	for _, item := range seqItems(mapEntry(m, "notifications")) {
		if item.Kind != yaml.MappingNode {
			continue
		}
		out = append(out, requiredHere(item, FileKindOrchestrator, []string{"notifications"})...)
		out = appendEnumLabeled(out, item, "type", NotificationTypeValues, "notification type")
	}
	for _, item := range seqItems(mapEntry(m, "git_credentials")) {
		if item.Kind == yaml.MappingNode {
			out = append(out, requiredHere(item, FileKindOrchestrator, []string{"git_credentials"})...)
		}
	}
	out = append(out, semanticOIDC(mapMapping(m, "oidc"))...)
	return out
}

// semanticOIDC validates the conditional oidc rules: everything is required only
// once an issuer is set (mirrors LoadOrchestratorConfig).
func semanticOIDC(o *yaml.Node) []Problem {
	if o == nil {
		return nil
	}
	if issuer, _ := scalarOf(mapEntry(o, "issuer")); issuer == "" {
		return nil
	}
	var out []Problem
	out = appendRequiredLabeled(out, o, "audience", "oidc.audience (required when oidc.issuer is set)")
	rm := mapMapping(o, "role_mapping")
	if rm == nil || len(rm.Content) == 0 {
		out = append(out, problemAt(o, "oidc.role_mapping must not be empty when oidc.issuer is set"))
		return out
	}
	for i := 0; i+1 < len(rm.Content); i += 2 {
		role := rm.Content[i+1]
		if v, ok := scalarOf(role); ok && !slices.Contains(OIDCRoleValues, v) {
			out = append(out, problemAt(role, fmt.Sprintf("invalid role %q (want %s)", v, strings.Join(OIDCRoleValues, ", "))))
		}
	}
	return out
}

// --- node helpers ---

// rootMapping unwraps a document node to its top-level mapping, or nil.
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		doc = doc.Content[0]
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

// mapEntry returns the value node for key in a mapping node, or nil.
func mapEntry(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapMapping returns the value node for key when it is itself a mapping, else nil.
func mapMapping(m *yaml.Node, key string) *yaml.Node {
	v := mapEntry(m, key)
	if v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	return nil
}

func seqItems(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

func scalarOf(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// hasNonEmpty reports whether key is present with a non-empty scalar/sequence/map.
func hasNonEmpty(m *yaml.Node, key string) bool {
	v := mapEntry(m, key)
	switch {
	case v == nil:
		return false
	case v.Kind == yaml.ScalarNode:
		return strings.TrimSpace(v.Value) != ""
	default:
		return len(v.Content) > 0
	}
}

func problemAt(n *yaml.Node, msg string) Problem {
	return Problem{Line: n.Line, Column: n.Column, Message: msg}
}

// requiredHere flags each of RequiredKeys(kind, path) that is missing/empty in m,
// positioned at the mapping (or list element) that should contain it.
func requiredHere(m *yaml.Node, kind FileKind, path []string) []Problem {
	var out []Problem
	for _, key := range RequiredKeys(kind, path) {
		out = appendRequiredLabeled(out, m, key, key)
	}
	return out
}

func appendRequiredLabeled(out []Problem, m *yaml.Node, key, label string) []Problem {
	if hasNonEmpty(m, key) {
		return out
	}
	return append(out, problemAt(m, label+" is required"))
}

func appendEnum(out []Problem, m *yaml.Node, key string, allowed []string) []Problem {
	return appendEnumLabeled(out, m, key, allowed, key)
}

func appendEnumLabeled(out []Problem, m *yaml.Node, key string, allowed []string, label string) []Problem {
	v := mapEntry(m, key)
	s, ok := scalarOf(v)
	if !ok || s == "" || slices.Contains(allowed, s) {
		return out
	}
	return append(out, problemAt(v, fmt.Sprintf("%s %q invalid (want %s)", label, s, strings.Join(allowed, ", "))))
}

func appendPort(out []Problem, m *yaml.Node, key string) []Problem {
	v := mapEntry(m, key)
	s, ok := scalarOf(v)
	if !ok || s == "" {
		return out
	}
	n, err := parseIntStrict(s)
	if err != nil || n < 0 || n > 65535 {
		return append(out, problemAt(v, fmt.Sprintf("%s %q out of range (1-65535)", key, s)))
	}
	return out
}

func parseIntStrict(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not an integer")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

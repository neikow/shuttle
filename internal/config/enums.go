package config

import (
	"sort"
	"strings"
)

// Enumerated value sets for the editor-facing tooling (completion + semantic
// validation in internal/lsp). Defined once, in terms of the same constants the
// loader/normalizers use, so the editor never drifts from what Shuttle accepts.
var (
	// UpdatePolicyValues are the accepted service `update_policy` values.
	UpdatePolicyValues = []string{UpdatePolicyRolling, UpdatePolicyRecreate}
	// DeleteVolumesValues are the canonical service `delete_volumes` values (a
	// human duration like "7 days" is also accepted, handled separately).
	DeleteVolumesValues = []string{DeleteVolumesManual, DeleteVolumesImmediate, "true", "false"}
	// BackupEngineValues are the accepted service `backup.engine` values.
	BackupEngineValues = []string{BackupEngineVolume, BackupEnginePostgres}
	// BackupStoreValues are the accepted `backup.store` / `backups.default_store`
	// values.
	BackupStoreValues = []string{BackupStoreLocal, BackupStoreRestic}
	// SecretsProviderValues are the accepted orchestrator `secrets_provider`
	// values.
	SecretsProviderValues = []string{"infisical", "file", "none"}
	// NotificationTypeValues are the accepted `notifications[].type` values.
	NotificationTypeValues = []string{NotifySlack, NotifyDiscord, NotifyWebhook}
	// OIDCRoleValues are the accepted control-plane roles in `oidc.role_mapping`.
	OIDCRoleValues = []string{"read", "deploy", "admin"}
)

// DNSProviderTypeNames returns the supported `dns.yml` provider `type` values,
// sorted. Backed by the dnsProviderSpecs registry so the editor lists exactly
// the types the shipped Caddy image can serve.
func DNSProviderTypeNames() []string {
	types := make([]string, 0, len(dnsProviderSpecs))
	for t := range dnsProviderSpecs {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// RequiredKeys returns the YAML keys that must be present at a nesting path
// within a file kind, used both to flag missing fields (semantic validation) and
// to mark a completion item "(required)". Keyed on the dotted nesting path so a
// list element (e.g. notifications) and a nested block (e.g. backups.env) each
// get their own set. Conditional requirements (external.upstream only when an
// external block exists, oidc.audience only when an issuer is set) are handled in
// semantic validation, not here. Returns nil when nothing is unconditionally
// required at the path.
func RequiredKeys(kind FileKind, path []string) []string {
	switch kind {
	case FileKindService:
		switch joinPath(path) {
		case "":
			return []string{"name", "host"}
		case "backup":
			return []string{"engine"}
		}
	case FileKindHosts:
		if joinPath(path) == "hosts" {
			return []string{"name"}
		}
	case FileKindDNS:
		switch joinPath(path) {
		case "providers":
			return []string{"name", "type"}
		case "certificates":
			return []string{"name", "domains", "provider"}
		case "zones":
			return []string{"domain", "provider"}
		}
	case FileKindOrchestrator:
		switch joinPath(path) {
		case "":
			return []string{"bearer_token"}
		case "notifications":
			return []string{"type", "url"}
		case "git_credentials":
			return []string{"repo_prefix", "infisical_key"}
		case "backups.env":
			return []string{"key", "infisical_key"}
		}
	}
	return nil
}

func joinPath(path []string) string {
	return strings.Join(path, ".")
}

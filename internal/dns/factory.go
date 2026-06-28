package dns

import "fmt"

// NewManager builds a record Manager for a dns.yml provider type. creds are the
// already-resolved credential values (the orchestrator resolves SecretRefs from
// the secrets provider before calling). endpoint is provider-specific (OVH
// region). Supported record types: "manual" (no-op) and "ovh"; the "sidecar"
// private-DNS type is added in a later change.
func NewManager(providerType, endpoint string, creds map[string]string) (Manager, error) {
	switch providerType {
	case "manual":
		return manualManager{}, nil
	case "ovh":
		return newOVHManager(endpoint, creds)
	default:
		return nil, fmt.Errorf("dns: unsupported record provider type %q", providerType)
	}
}

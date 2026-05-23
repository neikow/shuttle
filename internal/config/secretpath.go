package config

import "strings"

// DefaultSecretsBasePath is the shared secrets folder merged under every service
// when secrets_base_path is unset.
const DefaultSecretsBasePath = "/shared"

// isAbsSecretPath reports whether an Infisical folder path is absolute. Paths
// must be absolute so service folders can't be accidentally nested under each
// other; relative values are rejected at load.
func isAbsSecretPath(p string) bool {
	return strings.HasPrefix(p, "/")
}

// ResolveSecretsPaths returns the (base, service) Infisical folders to read for a
// service. base is the shared folder (secrets_base_path, default "/shared"); the
// service folder is its explicit secret_path, else the template with "{service}"
// substituted, else the base itself (no per-service folder configured).
func ResolveSecretsPaths(basePath, template, svcSecretPath, svcName string) (base, service string) {
	base = basePath
	if base == "" {
		base = DefaultSecretsBasePath
	}
	switch {
	case svcSecretPath != "":
		service = svcSecretPath
	case template != "":
		service = strings.ReplaceAll(template, "{service}", svcName)
	default:
		service = base
	}
	return base, service
}

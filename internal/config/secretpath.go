package config

import "strings"

// DefaultSecretsBasePath is the shared secrets folder merged under every service
// when secrets_base_path is unset.
const DefaultSecretsBasePath = "/shared"

// DefaultSecretsPathTemplate is the per-service folder derived from a service's
// name when secrets_path_template is unset. {service} is substituted with the
// service name.
const DefaultSecretsPathTemplate = "/services/{service}"

// isAbsSecretPath reports whether an Infisical folder path is absolute. Paths
// must be absolute so service folders can't be accidentally nested under each
// other; relative values are rejected at load.
func isAbsSecretPath(p string) bool {
	return strings.HasPrefix(p, "/")
}

// ResolveSecretsPaths returns the (base, service) Infisical folders to read for a
// service. base is the shared folder (secrets_base_path, default "/shared"); the
// service folder is its explicit secret_path, else the template with "{service}"
// substituted (the template defaults to "/services/{service}" when unset).
func ResolveSecretsPaths(basePath, template, svcSecretPath, svcName string) (base, service string) {
	base = basePath
	if base == "" {
		base = DefaultSecretsBasePath
	}
	if template == "" {
		template = DefaultSecretsPathTemplate
	}
	if svcSecretPath != "" {
		return base, svcSecretPath
	}
	return base, strings.ReplaceAll(template, "{service}", svcName)
}

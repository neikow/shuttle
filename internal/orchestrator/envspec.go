package orchestrator

import (
	"sort"
	"strings"
)

// envRef is an env-var source reference that could not be resolved. Scheme is
// "secret" (the configured secrets provider) or "env" (the orchestrator's
// process environment); Key is the missing key.
type envRef struct {
	Scheme string
	Key    string
}

func (r envRef) String() string { return r.Scheme + ":" + r.Key }

// schemeProvider reports whether a ${scheme:...} token reads from the configured
// secrets provider. An empty scheme (bare ${KEY}) and the provider aliases all
// resolve against the provider; "env" reads the process environment.
func schemeProvider(scheme string) bool {
	switch scheme {
	case "", "secret", "infisical", "file":
		return true
	default:
		return false
	}
}

// envUsesProvider reports whether any value in the env map needs a secrets
// provider lookup — an empty value (provider-keyed by the var name), a bare
// ${KEY}, or a ${secret/infisical/file:KEY} token. Pure literals and ${env:KEY}
// references need no provider, so a service using only those (or no env at all)
// requires no provider folder.
func envUsesProvider(env map[string]string) bool {
	for _, spec := range env {
		if valueUsesProvider(spec) {
			return true
		}
	}
	return false
}

func valueUsesProvider(spec string) bool {
	if spec == "" {
		return true // empty -> provider lookup of the var name
	}
	for _, tok := range envTokens(spec) {
		if schemeProvider(tok.scheme) {
			return true
		}
	}
	return false
}

type envToken struct {
	scheme string
	key    string
}

// envTokens extracts the ${scheme:KEY} references from a spec. A token without a
// colon is treated as a schemeless ${KEY}. Text outside ${...} is ignored here.
func envTokens(spec string) []envToken {
	var out []envToken
	rest := spec
	for {
		i := strings.Index(rest, "${")
		if i < 0 {
			return out
		}
		rest = rest[i+2:]
		j := strings.Index(rest, "}")
		if j < 0 {
			return out
		}
		body := rest[:j]
		rest = rest[j+1:]
		scheme, key, hasColon := strings.Cut(body, ":")
		if !hasColon {
			key = scheme
			scheme = ""
		}
		out = append(out, envToken{scheme: strings.TrimSpace(scheme), key: strings.TrimSpace(key)})
	}
}

// resolveEnv renders an env map into concrete values. provider holds the merged
// secrets (nil when none were fetched); getenv reads the process environment.
// Returns the resolved values and a sorted, de-duplicated list of references
// that could not be resolved (a missing provider key or unset ${env:KEY}).
func resolveEnv(env map[string]string, provider map[string]string, getenv func(string) (string, bool)) (map[string]string, []envRef) {
	out := make(map[string]string, len(env))
	var missing []envRef
	seen := map[string]bool{}
	addMissing := func(r envRef) {
		if !seen[r.String()] {
			seen[r.String()] = true
			missing = append(missing, r)
		}
	}
	lookupProvider := func(key string) (string, bool) {
		if provider == nil {
			return "", false
		}
		v, ok := provider[key]
		return v, ok
	}

	for name, spec := range env {
		// Empty spec -> provider, keyed by the variable name.
		if spec == "" {
			if v, ok := lookupProvider(name); ok {
				out[name] = v
			} else {
				addMissing(envRef{Scheme: "secret", Key: name})
			}
			continue
		}
		// No interpolation tokens -> literal value.
		if !strings.Contains(spec, "${") {
			out[name] = spec
			continue
		}
		out[name] = expand(spec, lookupProvider, getenv, addMissing)
	}

	sort.Slice(missing, func(i, j int) bool { return missing[i].String() < missing[j].String() })
	return out, missing
}

// expand substitutes every ${scheme:KEY} token in spec, keeping surrounding text
// verbatim. An unresolved token contributes an envRef via addMissing and expands
// to the empty string; a malformed ${ without a closing } is kept literally.
func expand(spec string, lookupProvider func(string) (string, bool), getenv func(string) (string, bool), addMissing func(envRef)) string {
	var b strings.Builder
	rest := spec
	for {
		i := strings.Index(rest, "${")
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		rest = rest[i+2:]
		j := strings.Index(rest, "}")
		if j < 0 {
			b.WriteString("${")
			b.WriteString(rest)
			return b.String()
		}
		body := rest[:j]
		rest = rest[j+1:]
		scheme, key, hasColon := strings.Cut(body, ":")
		if !hasColon {
			key = scheme
			scheme = ""
		}
		scheme = strings.TrimSpace(scheme)
		key = strings.TrimSpace(key)
		switch {
		case scheme == "env":
			if v, ok := getenv(key); ok {
				b.WriteString(v)
			} else {
				addMissing(envRef{Scheme: "env", Key: key})
			}
		case schemeProvider(scheme):
			if v, ok := lookupProvider(key); ok {
				b.WriteString(v)
			} else {
				addMissing(envRef{Scheme: "secret", Key: key})
			}
		default:
			addMissing(envRef{Scheme: scheme, Key: key})
		}
	}
}

// missingRefsString renders missing refs for an error message.
func missingRefsString(refs []envRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

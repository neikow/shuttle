package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// rollingWarnings inspects a compose file for configurations that prevent a
// zero-downtime rolling update from running two containers of a service at once:
// a fixed published host port (the second container's bind would clash) or a
// fixed container_name (Docker forbids two containers with the same name). These
// are warnings, not errors — the deploy still aborts safely at runtime, leaving
// the old version up — surfaced by `shuttle check` so the operator can switch
// the service to update_policy: recreate. Returns nil for recreate services or
// unparseable compose (compose itself reports the latter at deploy time).
func rollingWarnings(composeYAML []byte) []string {
	var doc struct {
		Services map[string]struct {
			ContainerName string      `yaml:"container_name"`
			Ports         []yaml.Node `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeYAML, &doc); err != nil {
		return nil
	}
	var warns []string
	for name, s := range doc.Services {
		if s.ContainerName != "" {
			warns = append(warns, fmt.Sprintf("compose service %q sets container_name; rolling update cannot run two instances (use update_policy: recreate)", name))
		}
		for _, p := range s.Ports {
			if portPublishesFixedHost(&p) {
				warns = append(warns, fmt.Sprintf("compose service %q publishes a fixed host port; rolling update would clash on the port bind (use update_policy: recreate)", name))
				break
			}
		}
	}
	sort.Strings(warns)
	return warns
}

// portPublishesFixedHost reports whether a compose ports entry binds a specific
// host port. Short form "8080:80" / "127.0.0.1:8080:80" → yes; "80" (container
// only, ephemeral host port) → no. Long form → yes when "published" is set.
func portPublishesFixedHost(node *yaml.Node) bool {
	switch node.Kind {
	case yaml.ScalarNode:
		parts := strings.Split(node.Value, ":")
		switch len(parts) {
		case 2: // host:container
			return parts[0] != ""
		case 3: // ip:host:container
			return parts[1] != ""
		default: // single "container" port → ephemeral host port
			return false
		}
	case yaml.MappingNode:
		var m map[string]any
		if err := node.Decode(&m); err != nil {
			return false
		}
		pub, ok := m["published"]
		if !ok {
			return false
		}
		s := strings.TrimSpace(fmt.Sprint(pub))
		return s != "" && s != "0"
	default:
		return false
	}
}

package config

import "fmt"

// Update policies control how an agent applies a deploy.
const (
	// UpdatePolicyRolling brings up new containers, waits for them to become
	// healthy, then removes the old ones — zero-downtime. It is the default.
	UpdatePolicyRolling = "rolling"
	// UpdatePolicyRecreate uses compose's stop-then-start recreate (brief
	// downtime). Use it for services that cannot run two instances at once
	// (e.g. those publishing a fixed host port, or holding an exclusive lock).
	UpdatePolicyRecreate = "recreate"
)

// normalizeUpdatePolicy validates and canonicalizes an update_policy string. An
// empty value defaults to rolling.
func normalizeUpdatePolicy(s string) (string, error) {
	switch s {
	case "":
		return UpdatePolicyRolling, nil
	case UpdatePolicyRolling, UpdatePolicyRecreate:
		return s, nil
	default:
		return "", fmt.Errorf("update_policy %q invalid (want %q or %q)", s, UpdatePolicyRolling, UpdatePolicyRecreate)
	}
}

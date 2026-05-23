package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Canonical delete_volumes policies. Any other value is a duration string
// (parseable by ParseHumanDuration) after which a removed service's named
// volumes are deleted.
const (
	DeleteVolumesImmediate = "immediate" // delete volumes as soon as the service is removed
	DeleteVolumesManual    = "manual"    // keep volumes until an explicit prune (default)
)

// deleteVolumesPolicy is the YAML form of a service's delete_volumes setting. It
// accepts a bool (true => immediate, false => manual), the strings
// "manual"/"immediate"/"true"/"false", or a human duration ("7 days", "30m"),
// and normalizes to a canonical string: "immediate", "manual", or the duration.
type deleteVolumesPolicy string

func (p *deleteVolumesPolicy) UnmarshalYAML(node *yaml.Node) error {
	switch node.Tag {
	case "!!bool":
		var b bool
		if err := node.Decode(&b); err != nil {
			return err
		}
		if b {
			*p = DeleteVolumesImmediate
		} else {
			*p = DeleteVolumesManual
		}
		return nil
	case "!!str":
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		norm, err := normalizeDeleteVolumes(s)
		if err != nil {
			return err
		}
		*p = deleteVolumesPolicy(norm)
		return nil
	default:
		return fmt.Errorf("delete_volumes must be a boolean or string, got %s", node.Tag)
	}
}

// normalizeDeleteVolumes validates and canonicalizes a delete_volumes string.
func normalizeDeleteVolumes(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "manual", "false":
		return DeleteVolumesManual, nil
	case "immediate", "true":
		return DeleteVolumesImmediate, nil
	}
	if _, err := ParseHumanDuration(s); err != nil {
		return "", fmt.Errorf("delete_volumes %q: want 'true', 'false', 'manual', or a duration like '7 days': %w", s, err)
	}
	return strings.TrimSpace(s), nil
}

// ParseHumanDuration parses a human-friendly duration: Go's own forms ("12h",
// "1h30m", "90s") plus spaced/spelled units such as "7 days", "2 weeks",
// "30 minutes", "1.5d". The result must be positive.
func ParseHumanDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return d, nil
	}

	numStr, unitStr := splitNumberUnit(s)
	if numStr == "" || unitStr == "" {
		return 0, fmt.Errorf("not a duration: %q", s)
	}
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("not a duration: %q", s)
	}
	var unit time.Duration
	switch strings.TrimSuffix(unitStr, "s") {
	case "sec", "second":
		unit = time.Second
	case "min", "minute":
		unit = time.Minute
	case "h", "hr", "hour":
		unit = time.Hour
	case "d", "day":
		unit = 24 * time.Hour
	case "w", "wk", "week":
		unit = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown duration unit %q", unitStr)
	}
	d := time.Duration(n * float64(unit))
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return d, nil
}

// splitNumberUnit splits a string like "7 days" or "1.5d" into ("7", "days").
func splitNumberUnit(s string) (num, unit string) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i:])
}

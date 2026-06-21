package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/neikow/shuttle/internal/config"
	"gopkg.in/yaml.v3"
)

// completeAt computes completion items for a cursor position in a Shuttle YAML
// document. It distinguishes key completion (offer the schema's field names valid
// at the cursor's nesting) from value completion (offer enum values, or
// cross-file references like host / certificate / provider names). Returns nil
// for an unrecognized file.
func completeAt(path, text string, pos position) []completionItem {
	kind := config.DetectFileKind(path)
	if kind == config.FileKindUnknown {
		return nil
	}
	lines := strings.Split(text, "\n")

	prefix := ""
	if pos.Line >= 0 && pos.Line < len(lines) {
		line := lines[pos.Line]
		c := max(0, min(pos.Character, len(line)))
		prefix = line[:c]
	}

	curIndent := leadingSpaces(prefix)
	path2 := parentPath(lines, pos.Line, curIndent)

	// Value context: the line already has "key:" before the cursor.
	content := strings.TrimLeft(prefix, " \t")
	content = strings.TrimPrefix(content, "- ")
	if key, _, isValue := strings.Cut(content, ":"); isValue {
		return valueCompletions(kind, path2, strings.TrimSpace(key), path, text)
	}

	// Key context: offer the field names valid at this nesting.
	names := config.FieldNamesAt(kind, path2)
	items := make([]completionItem, 0, len(names))
	for _, n := range names {
		items = append(items, completionItem{
			Label: n, Kind: ciField, InsertText: n + ": ", Detail: "shuttle " + string(kind),
		})
	}
	return items
}

// parentPath returns the YAML key nesting that contains the line at lineIdx,
// derived from indentation. List-item markers ("- ") don't contribute a key (the
// element's own fields are siblings, not parents), so a key inside a list element
// resolves to the list field. Best-effort, indentation-based.
func parentPath(lines []string, lineIdx, curIndent int) []string {
	var path []string
	minIndent := curIndent
	for i := lineIdx - 1; i >= 0; i-- {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpaces(raw)
		if indent >= minIndent {
			continue
		}
		content := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(content, "- ") {
			// List item: its container field is further up; add no key.
			minIndent = indent
			continue
		}
		if key, _, ok := strings.Cut(content, ":"); ok {
			path = append([]string{strings.TrimSpace(key)}, path...)
			minIndent = indent
			if indent == 0 {
				break
			}
		}
	}
	return path
}

// valueCompletions offers enum values or cross-file reference names for a known
// field. parent is the enclosing key path (to disambiguate same-named keys across
// blocks). filePath/text locate sibling files (hosts.yaml, dns.yml) and the live
// document.
func valueCompletions(kind config.FileKind, parent []string, key, filePath, text string) []completionItem {
	last := ""
	if len(parent) > 0 {
		last = parent[len(parent)-1]
	}
	switch kind {
	case config.FileKindService:
		switch key {
		case "update_policy":
			return enumItems("rolling", "recreate")
		case "delete_volumes":
			return enumItems("manual", "immediate", "true", "false")
		case "host":
			return refItems(namesFromSibling(filePath, "hosts.yaml", hostNames))
		case "tls_certificate":
			return refItems(namesFromSibling(filePath, "dns.yml", certNames))
		}
	case config.FileKindDNS:
		switch {
		case last == "providers" && key == "type":
			return enumItems("ovh")
		case last == "certificates" && key == "provider":
			return refItems(providerNames([]byte(text)))
		}
	case config.FileKindOrchestrator:
		switch key {
		case "secrets_provider":
			return enumItems("infisical", "file", "none")
		case "default_store":
			return enumItems("local", "restic")
		case "type": // notifications[].type
			return enumItems("slack", "discord", "webhook")
		}
	}
	return nil
}

func enumItems(values ...string) []completionItem {
	items := make([]completionItem, 0, len(values))
	for _, v := range values {
		items = append(items, completionItem{Label: v, Kind: ciValue, InsertText: v})
	}
	return items
}

func refItems(names []string) []completionItem {
	items := make([]completionItem, 0, len(names))
	for _, n := range names {
		items = append(items, completionItem{Label: n, Kind: ciProperty, InsertText: n})
	}
	return items
}

// namesFromSibling finds a file by walking up from the edited file's directory,
// reads it, and extracts names via parse.
func namesFromSibling(filePath, fileName string, parse func([]byte) []string) []string {
	dir := filepath.Dir(filePath)
	for {
		candidate := filepath.Join(dir, fileName)
		if data, err := os.ReadFile(candidate); err == nil {
			return parse(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil // reached filesystem root
		}
		dir = parent
	}
}

func hostNames(data []byte) []string {
	var f struct {
		Hosts []struct {
			Name string `yaml:"name"`
		} `yaml:"hosts"`
	}
	_ = yaml.Unmarshal(data, &f)
	return collectNames(len(f.Hosts), func(i int) string { return f.Hosts[i].Name })
}

func certNames(data []byte) []string {
	var f struct {
		Certificates []struct {
			Name string `yaml:"name"`
		} `yaml:"certificates"`
	}
	_ = yaml.Unmarshal(data, &f)
	return collectNames(len(f.Certificates), func(i int) string { return f.Certificates[i].Name })
}

func providerNames(data []byte) []string {
	var f struct {
		Providers []struct {
			Name string `yaml:"name"`
		} `yaml:"providers"`
	}
	_ = yaml.Unmarshal(data, &f)
	return collectNames(len(f.Providers), func(i int) string { return f.Providers[i].Name })
}

func collectNames(n int, at func(int) string) []string {
	var out []string
	for i := range n {
		if name := at(i); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' || r == '\t' {
			n++
			continue
		}
		break
	}
	return n
}

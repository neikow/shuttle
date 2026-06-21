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

	// Key context: offer the field names valid at this nesting, skipping keys
	// already present in the current block, annotated with their type + whether
	// the schema requires them.
	present := presentSiblings(lines, pos.Line, curIndent)
	fields := config.FieldsAt(kind, path2)
	items := make([]completionItem, 0, len(fields))
	for _, f := range fields {
		if present[f.Name] {
			continue
		}
		items = append(items, completionItem{
			Label: f.Name, Kind: ciField, InsertText: f.Name + ": ", Detail: fieldDetail(f),
		})
	}
	return items
}

// fieldDetail renders a completion item's detail: the field's type, marked
// "(required)" when the schema requires it at this nesting.
func fieldDetail(f config.FieldInfo) string {
	if f.Type == "" {
		if f.Required {
			return "required"
		}
		return ""
	}
	if f.Required {
		return f.Type + " (required)"
	}
	return f.Type
}

// presentSiblings collects the keys already declared in the mapping block that
// contains the cursor line (siblings at curIndent), so completion doesn't
// re-suggest them. Indentation-based, mirroring parentPath: a line that dedents
// past curIndent ends the block; a "- " at the lower indent contributes the list
// element's first inline field.
func presentSiblings(lines []string, lineIdx, curIndent int) map[string]bool {
	present := map[string]bool{}
	add := func(content string) {
		content = strings.TrimPrefix(content, "- ")
		if k, _, ok := strings.Cut(content, ":"); ok {
			present[strings.TrimSpace(k)] = true
		}
	}
	scan := func(start, step int) {
		for i := start; i >= 0 && i < len(lines); i += step {
			raw := lines[i]
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := leadingSpaces(raw)
			content := strings.TrimLeft(raw, " \t")
			switch {
			case indent == curIndent:
				if strings.HasPrefix(content, "- ") {
					return // a sibling list element, not a sibling key
				}
				add(content)
			case indent < curIndent:
				if strings.HasPrefix(content, "- ") {
					add(content) // the current element's inline first field
				}
				return
			}
		}
	}
	scan(lineIdx-1, -1)
	scan(lineIdx+1, +1)
	return present
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
			return enumItems(config.UpdatePolicyValues...)
		case "delete_volumes":
			return enumItems(config.DeleteVolumesValues...)
		case "host":
			return refItems(namesFromSibling(filePath, "hosts.yaml", hostNames))
		case "tls_certificate":
			return refItems(namesFromSibling(filePath, "dns.yml", certNames))
		}
		if last == "backup" {
			switch key {
			case "engine":
				return enumItems(config.BackupEngineValues...)
			case "store":
				return enumItems(config.BackupStoreValues...)
			}
		}
	case config.FileKindDNS:
		switch {
		case last == "providers" && key == "type":
			return enumItems(config.DNSProviderTypeNames()...)
		case last == "certificates" && key == "provider":
			return refItems(providerNames([]byte(text)))
		}
	case config.FileKindOrchestrator:
		switch key {
		case "secrets_provider":
			return enumItems(config.SecretsProviderValues...)
		case "default_store":
			return enumItems(config.BackupStoreValues...)
		case "type": // notifications[].type
			return enumItems(config.NotificationTypeValues...)
		}
	}
	// Generic fallback: any boolean-typed field offers true/false.
	if fieldHasType(kind, parent, key, "boolean") {
		return enumItems("true", "false")
	}
	return nil
}

// fieldHasType reports whether the field named key at the given nesting has the
// given friendly type (per config.FieldsAt) — used to offer true/false for any
// boolean field without enumerating each one.
func fieldHasType(kind config.FileKind, parent []string, key, typ string) bool {
	for _, f := range config.FieldsAt(kind, parent) {
		if f.Name == key {
			return f.Type == typ
		}
	}
	return false
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
	names, _ := siblingNames(filePath, fileName, parse)
	return names
}

// siblingNames is namesFromSibling that also reports whether the sibling file was
// found, so a caller can distinguish "no such file" (skip) from "file present but
// declares nothing" (an empty valid set).
func siblingNames(filePath, fileName string, parse func([]byte) []string) (names []string, found bool) {
	dir := filepath.Dir(filePath)
	for {
		candidate := filepath.Join(dir, fileName)
		if data, err := os.ReadFile(candidate); err == nil {
			return parse(data), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false // reached filesystem root
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

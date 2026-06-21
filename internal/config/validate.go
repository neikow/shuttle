package config

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileKind identifies which Shuttle YAML schema a file follows, so a tool (e.g.
// the language server) can validate and complete it without a repo on disk.
type FileKind string

const (
	FileKindUnknown          FileKind = ""
	FileKindHosts            FileKind = "hosts"             // hosts.yaml
	FileKindService          FileKind = "service"           // services/<name>/<name>.yaml
	FileKindDNS              FileKind = "dns"               // dns.yml
	FileKindOrchestrator     FileKind = "orchestrator"      // config.yml (bootstrap)
	FileKindRepoOrchestrator FileKind = "repo_orchestrator" // orchestrator.yaml (repo-managed)
)

// DetectFileKind classifies a file path within an IaC repo (or an orchestrator
// config.yml) by its name and location. Returns FileKindUnknown when the path is
// not a recognized Shuttle file.
func DetectFileKind(path string) FileKind {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	dir := filepath.ToSlash(filepath.Dir(path))
	switch {
	case base == "hosts.yaml":
		return FileKindHosts
	case base == "dns.yml":
		return FileKindDNS
	case base == "orchestrator.yaml":
		return FileKindRepoOrchestrator
	case base == "config.yml":
		return FileKindOrchestrator
	case strings.Contains(dir+"/", "/services/") && strings.HasSuffix(base, ".yaml"):
		// services/<name>/<name>.yaml — the service file is named after its dir.
		return FileKindService
	default:
		return FileKindUnknown
	}
}

// Problem is a positioned validation message (1-based line/column; column 0 when
// unknown), suitable for an editor diagnostic.
type Problem struct {
	Line    int
	Column  int
	Message string
}

// ValidateBytes strictly decodes a single file's content against the schema for
// its kind, returning every problem found (unknown keys, type mismatches, YAML
// syntax errors) with line numbers. It is single-file and disk-free, so it can
// run on an unsaved editor buffer. Referential checks that need the whole repo
// (host references, the compose-source XOR, secret resolution) are not done here
// — those come from a full config.Load. An empty/comment-only document is valid.
func ValidateBytes(kind FileKind, data []byte) []Problem {
	var target any
	switch kind {
	case FileKindHosts:
		target = &hostsFile{}
	case FileKindService:
		target = &serviceFile{}
	case FileKindDNS:
		target = &DNSConfig{}
	case FileKindOrchestrator:
		target = &OrchestratorConfig{}
	case FileKindRepoOrchestrator:
		target = &RepoOrchestratorConfig{}
	default:
		return nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(target)

	var structural []Problem
	if err != nil && !errors.Is(err, io.EOF) {
		structural = problemsFromYAMLError(err)
	}

	// Semantic checks (enums, required fields, intra-file references) run over the
	// parsed node tree for positions. Skip them when the document isn't even
	// parseable YAML — that's a pure syntax error, already in `structural`.
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return structural
	}
	return append(structural, semanticProblems(kind, &doc)...)
}

var yamlLineRe = regexp.MustCompile(`line (\d+):\s*`)

// problemsFromYAMLError turns a yaml.v3 decode error into positioned problems.
// A *yaml.TypeError carries one message per offending field; a plain error is a
// single syntax problem. Both encode the line as "line N:" in their text.
func problemsFromYAMLError(err error) []Problem {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		problems := make([]Problem, 0, len(typeErr.Errors))
		for _, msg := range typeErr.Errors {
			problems = append(problems, problemFromMessage(msg))
		}
		return problems
	}
	// Plain errors are prefixed "yaml: ".
	return []Problem{problemFromMessage(strings.TrimPrefix(err.Error(), "yaml: "))}
}

// rawStructFor returns the YAML-decoding struct type for a file kind, or nil.
func rawStructFor(kind FileKind) reflect.Type {
	switch kind {
	case FileKindHosts:
		return reflect.TypeFor[hostsFile]()
	case FileKindService:
		return reflect.TypeFor[serviceFile]()
	case FileKindDNS:
		return reflect.TypeFor[DNSConfig]()
	case FileKindOrchestrator:
		return reflect.TypeFor[OrchestratorConfig]()
	case FileKindRepoOrchestrator:
		return reflect.TypeFor[RepoOrchestratorConfig]()
	default:
		return nil
	}
}

// FieldNamesAt returns the YAML key names valid at the given nesting path within
// a file of the given kind, sorted. An empty path returns the top-level keys.
// The path follows yaml keys into nested structs (dereferencing pointers, slice,
// and map element types), so completion stays in lockstep with the Go structs.
// An unresolvable path returns nil.
func FieldNamesAt(kind FileKind, path []string) []string {
	t := rawStructFor(kind)
	if t == nil {
		return nil
	}
	for _, seg := range path {
		t = elemType(t)
		if t.Kind() != reflect.Struct {
			return nil
		}
		ft, ok := fieldTypeByYAMLName(t, seg)
		if !ok {
			return nil
		}
		t = ft
	}
	t = elemType(t)
	if t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < t.NumField(); i++ {
		if name := yamlName(t.Field(i)); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// FieldInfo describes a struct field for editor completion: its YAML key, a
// human-friendly type ("string", "integer", "boolean", "list", "mapping",
// "object"), and whether the schema requires it at this nesting.
type FieldInfo struct {
	Name     string
	Type     string
	Required bool
}

// FieldsAt is FieldNamesAt with each key's friendly type and required flag, so
// completion can show a useful detail and mark required fields. Same nesting
// rules and lockstep-with-the-structs guarantee as FieldNamesAt.
func FieldsAt(kind FileKind, path []string) []FieldInfo {
	t := rawStructFor(kind)
	if t == nil {
		return nil
	}
	for _, seg := range path {
		t = elemType(t)
		if t.Kind() != reflect.Struct {
			return nil
		}
		ft, ok := fieldTypeByYAMLName(t, seg)
		if !ok {
			return nil
		}
		t = ft
	}
	t = elemType(t)
	if t.Kind() != reflect.Struct {
		return nil
	}
	required := make(map[string]bool)
	for _, k := range RequiredKeys(kind, path) {
		required[k] = true
	}
	var out []FieldInfo
	for i := 0; i < t.NumField(); i++ {
		name := yamlName(t.Field(i))
		if name == "" {
			continue
		}
		out = append(out, FieldInfo{Name: name, Type: friendlyType(t.Field(i).Type), Required: required[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// friendlyType maps a struct field type to a short YAML-ish type name.
func friendlyType(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Map:
		return "mapping"
	case reflect.Struct:
		return "object"
	default:
		return ""
	}
}

// elemType unwraps pointer, slice, array, and map types to their element type so
// a nesting path can descend into `[]Struct` / `map[string]Struct` / `*Struct`.
func elemType(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
		default:
			return t
		}
	}
}

func fieldTypeByYAMLName(t reflect.Type, name string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		if yamlName(t.Field(i)) == name {
			return t.Field(i).Type, true
		}
	}
	return reflect.TypeOf(nil), false
}

// yamlName extracts a struct field's YAML key, or "" when it is unexported or
// tagged "-". A missing tag falls back to the lowercased field name (yaml.v3's
// default), matching how these structs decode.
func yamlName(f reflect.StructField) string {
	if f.PkgPath != "" { // unexported
		return ""
	}
	tag := f.Tag.Get("yaml")
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func problemFromMessage(msg string) Problem {
	msg = strings.TrimSpace(msg)
	line := 1
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			line = n
		}
		msg = strings.TrimSpace(yamlLineRe.ReplaceAllString(msg, ""))
	}
	return Problem{Line: line, Message: msg}
}

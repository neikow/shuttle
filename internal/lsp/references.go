package lsp

import (
	"fmt"
	"slices"

	"github.com/neikow/shuttle/internal/config"
	"gopkg.in/yaml.v3"
)

// crossFileDiagnostics flags references in a service file that point outside the
// buffer: `host` must name an entry in a sibling hosts.yaml, and
// `tls_certificate` a certificate in a sibling dns.yml. These need files on disk,
// so they live here rather than in the disk-free config.ValidateBytes. Each check
// is skipped when its sibling file can't be found (the reference can't be judged
// from the buffer alone) to avoid false positives while editing standalone.
func crossFileDiagnostics(path, text string) []diagnostic {
	if config.DetectFileKind(path) != config.FileKindService {
		return nil
	}
	root := docMapping([]byte(text))
	if root == nil {
		return nil
	}
	var diags []diagnostic
	if hosts, found := siblingNames(path, "hosts.yaml", hostNames); found {
		diags = appendRefDiag(diags, root, "host", hosts, "host", "hosts.yaml")
	}
	if certs, found := siblingNames(path, "dns.yml", certNames); found {
		diags = appendRefDiag(diags, root, "tls_certificate", certs, "certificate", "dns.yml")
	}
	return diags
}

// appendRefDiag flags root[key] when it names something not in valid.
func appendRefDiag(diags []diagnostic, root *yaml.Node, key string, valid []string, label, file string) []diagnostic {
	v := nodeEntry(root, key)
	if v == nil || v.Kind != yaml.ScalarNode || v.Value == "" || slices.Contains(valid, v.Value) {
		return diags
	}
	return append(diags, diagnostic{
		Range:    nodeRange(v),
		Severity: sevError,
		Source:   "shuttle",
		Message:  fmt.Sprintf("unknown %s %q (not declared in %s)", label, v.Value, file),
	})
}

// docMapping parses YAML and returns the top-level mapping node, or nil.
func docMapping(data []byte) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	n := &doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// nodeEntry returns the value node for key in a mapping node, or nil.
func nodeEntry(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// nodeRange is a tight range over a scalar node's value (1-based node position
// converted to 0-based LSP).
func nodeRange(n *yaml.Node) rangeT {
	line := max(n.Line-1, 0)
	col := max(n.Column-1, 0)
	return rangeT{
		Start: position{Line: line, Character: col},
		End:   position{Line: line, Character: col + len(n.Value)},
	}
}

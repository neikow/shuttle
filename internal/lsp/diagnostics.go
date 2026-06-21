package lsp

import (
	"strings"

	"github.com/neikow/shuttle/internal/config"
)

// diagnosticsFor validates a document's content against the schema for its file
// kind and returns LSP diagnostics. A file Shuttle doesn't recognize yields nil
// (no diagnostics). Validation is single-file and disk-free (it runs on the live
// buffer); whole-repo referential checks are out of scope here.
func diagnosticsFor(path, text string) []diagnostic {
	kind := config.DetectFileKind(path)
	if kind == config.FileKindUnknown {
		return nil
	}
	lines := strings.Split(text, "\n")
	var diags []diagnostic
	for _, p := range config.ValidateBytes(kind, []byte(text)) {
		line := max(p.Line-1, 0) // config reports 1-based lines
		start := max(p.Column-1, 0)
		end := start + 1
		if line < len(lines) {
			end = len(lines[line])
		}
		diags = append(diags, diagnostic{
			Range: rangeT{
				Start: position{Line: line, Character: start},
				End:   position{Line: line, Character: end},
			},
			Severity: sevError,
			Source:   "shuttle",
			Message:  p.Message,
		})
	}
	// Cross-file reference checks read sibling files (host/tls_certificate), so
	// they live outside the disk-free config.ValidateBytes.
	diags = append(diags, crossFileDiagnostics(path, text)...)
	if diags == nil {
		return []diagnostic{}
	}
	return diags
}

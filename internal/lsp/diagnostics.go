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
	problems := config.ValidateBytes(kind, []byte(text))
	if len(problems) == 0 {
		return []diagnostic{}
	}
	lines := strings.Split(text, "\n")
	diags := make([]diagnostic, 0, len(problems))
	for _, p := range problems {
		line := max(p.Line-1, 0) // config reports 1-based lines
		end := 1
		if line < len(lines) {
			end = len(lines[line])
		}
		diags = append(diags, diagnostic{
			Range: rangeT{
				Start: position{Line: line, Character: 0},
				End:   position{Line: line, Character: end},
			},
			Severity: sevError,
			Source:   "shuttle",
			Message:  p.Message,
		})
	}
	return diags
}

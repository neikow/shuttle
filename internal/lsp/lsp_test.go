package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func labels(items []completionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

const svcPath = "/repo/services/api/api.yaml"

func TestCompleteAt_topLevelKeys(t *testing.T) {
	// Empty third line at the top level → service field names.
	text := "name: api\nhost: web1\n"
	items := completeAt(svcPath, text, position{Line: 2, Character: 0})
	got := labels(items)
	for _, want := range []string{"host", "domains", "external", "tls_certificate"} {
		if !slices.Contains(got, want) {
			t.Errorf("top-level keys missing %q (got %v)", want, got)
		}
	}
}

func TestCompleteAt_enumValue(t *testing.T) {
	items := completeAt(svcPath, "update_policy: ", position{Line: 0, Character: 15})
	if got := labels(items); !slices.Equal(got, []string{"rolling", "recreate"}) {
		t.Errorf("update_policy values = %v, want [rolling recreate]", got)
	}
}

func TestCompleteAt_nestedKeys(t *testing.T) {
	// Inside the external: block → its only field.
	text := "external:\n  "
	items := completeAt(svcPath, text, position{Line: 1, Character: 2})
	if got := labels(items); !slices.Equal(got, []string{"upstream"}) {
		t.Errorf("external keys = %v, want [upstream]", got)
	}
}

func TestCompleteAt_dnsProviderRef(t *testing.T) {
	// In a dns.yml certificate's `provider:` value → provider names from the doc.
	text := "providers:\n  - name: ovh\n    type: ovh\ncertificates:\n  - name: star\n    provider: "
	items := completeAt("/repo/dns.yml", text, position{Line: 5, Character: 14})
	if got := labels(items); !slices.Contains(got, "ovh") {
		t.Errorf("provider ref = %v, want to contain ovh", got)
	}
}

func TestCompleteAt_hostRefFromSibling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte("hosts:\n  - name: web1\n  - name: web2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svcDir, "api.yaml")

	items := completeAt(path, "host: ", position{Line: 0, Character: 6})
	got := labels(items)
	if !slices.Contains(got, "web1") || !slices.Contains(got, "web2") {
		t.Errorf("host refs = %v, want web1 + web2 from sibling hosts.yaml", got)
	}
}

func TestCompleteAt_unknownFile(t *testing.T) {
	if items := completeAt("/repo/README.md", "x", position{}); items != nil {
		t.Errorf("unknown file should yield nil, got %v", items)
	}
}

func TestDiagnosticsFor(t *testing.T) {
	// Unknown key → one diagnostic on its line (host present so no required-field
	// diagnostic competes).
	diags := diagnosticsFor(svcPath, "name: api\nhost: web1\nbogus: x\n")
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if diags[0].Range.Start.Line != 2 {
		t.Errorf("diagnostic line = %d, want 2", diags[0].Range.Start.Line)
	}
	// Valid file → no diagnostics. Unknown file kind → nil.
	if d := diagnosticsFor(svcPath, "name: api\nhost: web1\n"); len(d) != 0 {
		t.Errorf("valid service should have no diagnostics, got %+v", d)
	}
	if d := diagnosticsFor("/repo/README.md", "anything"); d != nil {
		t.Errorf("unknown file kind → nil, got %+v", d)
	}
}

// --- server round-trip ---

func frame(t *testing.T, buf *bytes.Buffer, id int, method string, params any) {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	msg := &rpcMessage{Method: method, Params: raw}
	if id != 0 {
		msg.ID = json.RawMessage(strconv.Itoa(id))
	}
	if err := writeMessage(buf, msg); err != nil {
		t.Fatal(err)
	}
}

func TestServer_roundtrip(t *testing.T) {
	var in bytes.Buffer
	frame(t, &in, 1, "initialize", map[string]any{})
	frame(t, &in, 0, "textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: "file:///repo/services/api/api.yaml", Text: "name: api\nhost: web1\nbogus: x\n"},
	})
	frame(t, &in, 0, "exit", nil)

	var out bytes.Buffer
	if err := NewServer(&in, &out, "test").Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expect an initialize response and a publishDiagnostics notification.
	r := bufio.NewReader(&out)
	sawInit, sawDiag := false, false
	for {
		msg, err := readMessage(r)
		if err != nil {
			break
		}
		if len(msg.ID) > 0 && msg.Result != nil {
			sawInit = true
		}
		if msg.Method == "textDocument/publishDiagnostics" {
			var p publishDiagnosticsParams
			if err := json.Unmarshal(msg.Params, &p); err == nil && len(p.Diagnostics) == 1 {
				sawDiag = true
			}
		}
	}
	if !sawInit {
		t.Error("no initialize response")
	}
	if !sawDiag {
		t.Error("no publishDiagnostics with the expected 1 problem")
	}
}

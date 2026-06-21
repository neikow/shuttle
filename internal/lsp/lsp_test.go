package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	// Empty third line at the top level → service field names not already present.
	text := "name: api\nhost: web1\n"
	items := completeAt(svcPath, text, position{Line: 2, Character: 0})
	got := labels(items)
	for _, want := range []string{"domains", "external", "tls_certificate"} {
		if !slices.Contains(got, want) {
			t.Errorf("top-level keys missing %q (got %v)", want, got)
		}
	}
}

func TestCompleteAt_skipsPresentKeys(t *testing.T) {
	// name + host already present → not re-suggested; domains still offered.
	text := "name: api\nhost: web1\n"
	got := labels(completeAt(svcPath, text, position{Line: 2, Character: 0}))
	if slices.Contains(got, "name") || slices.Contains(got, "host") {
		t.Errorf("present keys should be filtered out, got %v", got)
	}
	if !slices.Contains(got, "domains") {
		t.Errorf("domains should still be offered, got %v", got)
	}
}

func TestCompleteAt_detailTypeAndRequired(t *testing.T) {
	items := completeAt(svcPath, "", position{Line: 0, Character: 0})
	byLabel := map[string]completionItem{}
	for _, it := range items {
		byLabel[it.Label] = it
	}
	if d := byLabel["host"].Detail; d != "string (required)" {
		t.Errorf("host detail = %q, want %q", d, "string (required)")
	}
	if d := byLabel["port"].Detail; d != "integer" {
		t.Errorf("port detail = %q, want %q", d, "integer")
	}
}

func TestCompleteAt_genericBoolValue(t *testing.T) {
	// before_deploy is a bool field inside backup: → true/false offered without
	// being enumerated explicitly.
	text := "backup:\n  before_deploy: "
	got := labels(completeAt(svcPath, text, position{Line: 1, Character: len("  before_deploy: ")}))
	if !slices.Equal(got, []string{"true", "false"}) {
		t.Errorf("before_deploy values = %v, want [true false]", got)
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

func TestCrossFileDiagnostics_host(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte("hosts:\n  - name: web1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svcDir, "api.yaml")

	// Unknown host → a cross-file diagnostic.
	diags := diagnosticsFor(path, "name: api\nhost: nope\n")
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown host") {
			found = true
		}
	}
	if !found {
		t.Errorf("want an unknown-host diagnostic, got %+v", diags)
	}

	// Known host → no cross-file diagnostic.
	for _, d := range diagnosticsFor(path, "name: api\nhost: web1\n") {
		if strings.Contains(d.Message, "unknown host") {
			t.Errorf("web1 is declared; should not be flagged: %+v", d)
		}
	}
}

func TestCrossFileDiagnostics_noSiblingNoFalsePositive(t *testing.T) {
	// No hosts.yaml on disk → the host reference can't be judged, so it isn't
	// flagged (avoids false positives while editing standalone).
	for _, d := range diagnosticsFor("/nowhere/services/api/api.yaml", "name: api\nhost: web1\n") {
		if strings.Contains(d.Message, "unknown host") {
			t.Errorf("missing hosts.yaml should not produce a host diagnostic: %+v", d)
		}
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

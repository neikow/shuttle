// Package lsp implements a minimal Language Server Protocol server for Shuttle's
// IaC YAML files (hosts.yaml, services/*/*.yaml, dns.yml, orchestrator.yaml, and
// the orchestrator config.yml). It provides schema-aware completion and live
// validation, reusing internal/config so the editor experience stays in lockstep
// with the loader. The transport is stdio JSON-RPC 2.0 (LSP framing), hand-rolled
// to avoid a dependency — only the handful of methods an editor needs are
// implemented.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// rpcMessage is a JSON-RPC 2.0 envelope covering requests, responses, and
// notifications (an absent id means a notification).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- LSP types (the subset this server uses) ---

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type rangeT struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type contentChange struct {
	Text string `json:"text"` // full-document sync
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange        `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type diagnostic struct {
	Range    rangeT `json:"range"`
	Severity int    `json:"severity"` // 1 error, 2 warning, 3 info, 4 hint
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type completionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"` // 5 field, 6 variable, 12 value, 10 property
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync   int                `json:"textDocumentSync"` // 1 = full
	CompletionProvider *completionOptions `json:"completionProvider,omitempty"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Completion item kinds (LSP CompletionItemKind subset).
const (
	ciField    = 5
	ciValue    = 12
	ciProperty = 10
)

// Diagnostic severities.
const (
	sevError = 1
)

// readMessage reads one LSP-framed JSON-RPC message (Content-Length header + body).
func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	var contentLen int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if name, val, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			contentLen, err = strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %w", err)
			}
		}
	}
	if contentLen <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	return &msg, nil
}

// writeMessage writes one LSP-framed JSON-RPC message.
func writeMessage(w io.Writer, msg *rpcMessage) error {
	msg.JSONRPC = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// uriToPath converts a file:// URI to a local filesystem path.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	p := u.Path
	// Windows: /C:/foo -> C:/foo. Harmless on POSIX.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

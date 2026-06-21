package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// Server is a single-client LSP server over a reader/writer pair (stdio). It is
// driven by one goroutine — read a message, handle it, write any reply — so the
// document store needs no locking.
type Server struct {
	r       *bufio.Reader
	w       io.Writer
	version string

	docs   map[string]string // uri -> current text
	closed bool
}

// NewServer builds a server reading from in and writing to out.
func NewServer(in io.Reader, out io.Writer, version string) *Server {
	return &Server{
		r:       bufio.NewReader(in),
		w:       out,
		version: version,
		docs:    map[string]string{},
	}
}

// Run processes messages until the client sends `exit` (or the input closes).
func (s *Server) Run() error {
	for {
		msg, err := readMessage(s.r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := s.handle(msg); err != nil {
			return err
		}
		if s.closed {
			return nil
		}
	}
}

func (s *Server) handle(msg *rpcMessage) error {
	switch msg.Method {
	case "initialize":
		return s.reply(msg.ID, initializeResult{
			Capabilities: serverCapabilities{
				TextDocumentSync:   1, // full sync
				CompletionProvider: &completionOptions{TriggerCharacters: []string{":", " ", "-"}},
			},
			ServerInfo: serverInfo{Name: "shuttle-lsp", Version: s.version},
		})
	case "initialized":
		return nil // notification, no reply
	case "textDocument/didOpen":
		var p didOpenParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil
		}
		s.docs[p.TextDocument.URI] = p.TextDocument.Text
		return s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didChange":
		var p didChangeParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil
		}
		if n := len(p.ContentChanges); n > 0 {
			s.docs[p.TextDocument.URI] = p.ContentChanges[n-1].Text // full sync
		}
		return s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didSave":
		var p didSaveParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil
		}
		if p.Text != nil {
			s.docs[p.TextDocument.URI] = *p.Text
		}
		return s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didClose":
		var p didCloseParams
		if err := json.Unmarshal(msg.Params, &p); err == nil {
			delete(s.docs, p.TextDocument.URI)
			// Clear diagnostics for the closed file.
			_ = s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []diagnostic{}})
		}
		return nil
	case "textDocument/completion":
		var p completionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return s.reply(msg.ID, []completionItem{})
		}
		items := s.complete(p.TextDocument.URI, p.Position)
		return s.reply(msg.ID, items)
	case "shutdown":
		return s.reply(msg.ID, nil)
	case "exit":
		s.closed = true
		return nil
	default:
		// Unknown request → empty result; unknown notification → ignore.
		if len(msg.ID) > 0 {
			return s.reply(msg.ID, nil)
		}
		return nil
	}
}

// complete computes completion items for the document at the given position.
func (s *Server) complete(uri string, pos position) []completionItem {
	text, ok := s.docs[uri]
	if !ok {
		return []completionItem{}
	}
	items := completeAt(uriToPath(uri), text, pos)
	if items == nil {
		return []completionItem{}
	}
	return items
}

// publishDiagnostics validates the document and pushes diagnostics to the client.
func (s *Server) publishDiagnostics(uri string) error {
	text := s.docs[uri]
	diags := diagnosticsFor(uriToPath(uri), text)
	if diags == nil {
		diags = []diagnostic{}
	}
	return s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

func (s *Server) reply(id json.RawMessage, result any) error {
	if len(id) == 0 {
		return nil // not a request; nothing to reply to
	}
	return writeMessage(s.w, &rpcMessage{ID: id, Result: result})
}

func (s *Server) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return writeMessage(s.w, &rpcMessage{Method: method, Params: raw})
}

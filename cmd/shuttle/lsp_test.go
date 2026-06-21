package main

import "testing"

// LSP clients (e.g. vscode-languageclient with TransportKind.stdio) append a
// --stdio argument to the server command. The command must accept it rather than
// failing with "unknown flag", which would crash-loop the client.
func TestLSPCommandAcceptsStdioFlag(t *testing.T) {
	if lspCmd.Flags().Lookup("stdio") == nil {
		t.Fatal("lsp command is missing the --stdio flag")
	}
	if err := lspCmd.Flags().Parse([]string{"--stdio"}); err != nil {
		t.Fatalf("parsing --stdio: %v", err)
	}
}

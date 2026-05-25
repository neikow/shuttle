//go:build !embedui

// Package web provides the UI-embedding hooks. Without the `embedui` build tag
// no bundle is embedded (so a plain `go build ./...` needs no web/dist), and the
// orchestrator skips serving /ui.
package web

import "io/fs"

// Enabled reports whether the UI bundle was embedded at build time.
const Enabled = false

// FS returns nil when the UI was not embedded.
func FS() fs.FS { return nil }

//go:build embedui

// Package web embeds the production React UI bundle so the orchestrator can
// serve it from the single binary. Built only with the `embedui` tag (set by
// `make build` / release), after `make web` has produced web/dist.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Enabled reports whether the UI bundle was embedded at build time.
const Enabled = true

// FS returns the embedded dist/ rooted so index.html sits at the top.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

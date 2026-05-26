package orchestrator

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/neikow/shuttle/web"
)

// EnableUI serves the embedded web UI under /ui/ (no-op unless the binary was
// built with the `embedui` tag). The static bundle is served unauthenticated —
// the browser app authenticates its own API calls with the bearer token the
// user pastes, so the control-plane endpoints stay protected by bearerAuth.
func (s *HTTPServer) EnableUI() {
	if !web.Enabled {
		return
	}
	uiFS := web.FS()
	s.mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	s.mux.Handle("GET /ui/", http.StripPrefix("/ui/", spaHandler(uiFS)))
}

// spaHandler serves files from uiFS, falling back to index.html for paths that
// don't map to a real asset (client-side routing / deep links).
func spaHandler(uiFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(uiFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := uiFS.Open(p); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

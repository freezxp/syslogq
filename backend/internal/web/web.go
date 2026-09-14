// Package web embeds the built SPA (frontend/ → dist via Vite) and serves
// it with an index.html fallback for client-side routes (ADR-0006). The
// binary ships the UI; `make web` refreshes backend/internal/web/dist.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the SPA. Asset paths under /assets/ are content-hashed,
// so they get immutable caching; everything else falls back to index.html
// for client-side routing. API routes never reach here — the mux routes
// them before "/".
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: embedded dist missing: " + err.Error())
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: serve index.html for client-routed paths.
		w.Header().Set("Cache-Control", "no-cache")
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}

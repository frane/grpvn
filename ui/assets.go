// Package ui contains the shared browser and Wails frontend. Built assets are
// checked in so CLI/server builds need only Go; npm is needed to change the UI.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var assets embed.FS

func Handler() http.Handler {
	root, _ := fs.Sub(assets, "dist")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		files.ServeHTTP(w, r)
	})
}

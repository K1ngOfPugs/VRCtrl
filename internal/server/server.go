// Package server serves the phone web app, the videos, and the sync socket.
package server

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"

	"vrctrl/internal/library"
)

//go:embed web
var webFS embed.FS

// New returns the HTTP handler for phones.
func New(lib *library.Library, hub *Hub) http.Handler {
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.HandleFunc("/video/{id}", func(w http.ResponseWriter, r *http.Request) {
		path, mime, ok := lib.Path(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "", fi.ModTime(), f)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".test.mjs") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
	return mux
}

// Listen binds addr (so a busy port is reported immediately) and serves h in
// the background.
func Listen(addr string, h http.Handler) (*http.Server, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &http.Server{Handler: h}
	go s.Serve(l)
	return s, nil
}

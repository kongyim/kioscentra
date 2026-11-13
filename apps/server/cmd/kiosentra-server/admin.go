package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func withAdmin(api http.Handler, directory string) (http.Handler, error) {
	if directory == "" {
		return api, nil
	}
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("admin directory must be absolute")
	}
	types := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "core.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8"}
	assets := map[string][]byte{}
	for file := range types {
		b, err := os.ReadFile(filepath.Join(directory, file))
		if err != nil {
			return nil, fmt.Errorf("read admin asset %s: %w", file, err)
		}
		assets[file] = b
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/admin" {
			http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			api.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		file := strings.TrimPrefix(r.URL.Path, "/admin/")
		if file == "" {
			file = "index.html"
		}
		b, ok := assets[file]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", types[file])
		if r.Method == "GET" {
			_, _ = w.Write(b)
		}
	}), nil
}

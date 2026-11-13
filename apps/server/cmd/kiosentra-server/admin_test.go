package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminStaticBoundary(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"index.html", "app.js", "core.js", "style.css", "private.env"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	h, err := withAdmin(api, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		path, method string
		status       int
	}{{"/admin/", "GET", 200}, {"/admin/app.js", "GET", 200}, {"/admin/", "HEAD", 200}, {"/admin/private.env", "GET", 404}, {"/admin/../private.env", "GET", 404}, {"/admin/", "POST", 405}, {"/api/v1/admin/devices", "GET", 401}, {"/", "GET", 307}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(v.method, v.path, nil))
		if w.Code != v.status {
			t.Fatalf("%s %s: %d", v.method, v.path, w.Code)
		}
		if strings.HasPrefix(v.path, "/admin/") && !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing CSP")
		}
		if v.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned body")
		}
	}
	if _, err = withAdmin(api, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing assets accepted")
	}
}

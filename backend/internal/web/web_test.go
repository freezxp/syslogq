package web

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAndFallback(t *testing.T) {
	h := Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /: %d, want 200 (index.html)", rec.Code)
	}

	// Client-side route falls back to index.html with no-cache.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/explorer?q=x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /explorer: %d, want 200 (SPA fallback)", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("fallback Cache-Control = %q", cc)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Error("fallback returned empty body")
	}

	// Path traversal must never serve files outside dist; the embed FS
	// rejects ".." paths, so the worst case is the index.html fallback.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../etc/passwd", nil))
	body, _ = io.ReadAll(rec.Body)
	if strings.Contains(string(body), "root:") {
		t.Error("traversal leaked /etc/passwd")
	}
}

func TestHandlerCachesHashedAssets(t *testing.T) {
	matches, err := fs.Glob(distFS, "dist/assets/*.js")
	if err != nil || len(matches) == 0 {
		t.Skip("no built assets (placeholder dist)")
	}
	name := strings.TrimPrefix(matches[0], "dist/")

	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /%s: %d", name, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", cc)
	}
}

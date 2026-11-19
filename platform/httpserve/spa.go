package httpserve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SPAHandler serves root as a single-page app: existing files are served as
// assets, extensionless paths fall back to index.html, and a missing path that
// looks like an asset is a real 404 rather than a silently mismatched page.
func SPAHandler(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		file := filepath.Join(root, clean)
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			setAssetCacheHeaders(w, clean)
			fs.ServeHTTP(w, r)
			return
		}
		if hasExtension(clean) {
			http.NotFound(w, r)
			return
		}
		index, err := os.ReadFile(filepath.Join(root, "index.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// setAssetCacheHeaders marks hashed SvelteKit assets immutable; everything
// else revalidates.
func setAssetCacheHeaders(w http.ResponseWriter, path string) {
	if strings.HasPrefix(path, "/_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// hasExtension reports whether the last path segment names a file. SPA routes
// are extensionless by convention; an extension signals an asset request.
func hasExtension(path string) bool {
	base := path[strings.LastIndexByte(path, '/')+1:]
	return strings.Contains(base, ".") && !strings.HasPrefix(base, ".")
}

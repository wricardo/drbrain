// Package api serves the built web client. It deliberately contains no game
// logic and no GraphQL wiring: the HTTP API lives in package graph, and this
// package exists only so the optional UI has somewhere to be mounted from.
//
// The single rule worth knowing is that the UI is optional. StaticHandler
// reports whether a build was found, and the server mounts the root route only
// if it was, so a headless deployment (an agent driving the game over GraphQL,
// a CI job, a fresh clone that has not run `npm run build`) serves the API and
// nothing else rather than a 404 at `/`.
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StaticHandler serves the built SvelteKit app with a single-page fallback.
//
// The UI is optional: if the build directory is absent (a fresh clone, or a
// headless deployment that only needs the API) this returns nil and the caller
// simply does not mount a root route. The API stays fully usable either way,
// which is the point of the "API is the game" rule.
func StaticHandler(dir string) (http.Handler, bool) {
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		return nil, false
	}

	fs := http.Dir(dir)
	fileServer := http.FileServer(fs)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only GET/HEAD can serve files.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		clean := filepath.Clean(r.URL.Path)
		if clean == "/" {
			serveFile(w, r, index)
			return
		}

		// Immutable build assets get a long cache; anything else may change
		// between deploys.
		if strings.HasPrefix(clean, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		// A real file wins; otherwise fall back to the SPA shell so client-side
		// routes survive a refresh.
		if st, err := os.Stat(filepath.Join(dir, clean)); err == nil && !st.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveFile(w, r, index)
	}), true
}

// serveFile writes a file with the right content type, without exposing the
// filesystem to directory traversal (the path is always one we constructed).
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

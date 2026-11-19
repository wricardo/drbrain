package host

import (
	"bytes"
	"net/http"
	"strings"
	"text/template"
)

// llmsHandler renders a game's agent guide at /<prefix>/llms.txt. The game
// supplies the template; the platform owns the route and the base URL.
//
// publicURL is the operator-configured base (platform or game), without the
// prefix. When empty, the request's own scheme and host are used, so local and
// proxied deployments point at the server that actually answered.
func llmsHandler(tmpl *template.Template, prefix, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimRight(publicURL, "/")
		if base == "" {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			base = scheme + "://" + r.Host
		}
		base += prefix

		data := struct{ BaseURL, WSURL string }{
			BaseURL: base,
			WSURL:   "ws" + strings.TrimPrefix(base, "http"),
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(buf.Bytes())
	})
}

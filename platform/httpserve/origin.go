package httpserve

import (
	"log"
	"net/http"
	"strings"

	gorillaws "github.com/gorilla/websocket"
)

// ParseAllowedOrigins splits the comma-separated ALLOWED_ORIGINS value. Empty
// or "*" yields nil, meaning every origin is allowed.
func ParseAllowedOrigins(allowedOrigins string) []string {
	var allowed []string
	if allowedOrigins != "" && allowedOrigins != "*" {
		for _, o := range strings.Split(allowedOrigins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed = append(allowed, o)
			}
		}
	}
	return allowed
}

// CheckOrigin allows every origin when allowed is empty, otherwise an exact
// match. Requests without an Origin header (non-browser clients) are allowed.
func CheckOrigin(allowed []string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if len(allowed) == 0 {
			return true
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		for _, a := range allowed {
			if a == origin {
				return true
			}
		}
		log.Printf("websocket: rejected origin %q (not in ALLOWED_ORIGINS)", origin)
		return false
	}
}

// Upgrader builds the websocket upgrader the GraphQL surface uses. The origin
// check is the same one CORS applies, so a browser origin cannot bypass one
// surface while being blocked on the other.
func Upgrader(allowedOrigins string) gorillaws.Upgrader {
	return gorillaws.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     CheckOrigin(ParseAllowedOrigins(allowedOrigins)),
	}
}

// WithCORS answers CORS preflights and sets the response headers for allowed
// origins. Credentials are deliberately not enabled: the login cookie still
// needs a same-origin check in the viewer middleware.
func WithCORS(allowedOrigins string, next http.Handler) http.Handler {
	allowed := ParseAllowedOrigins(allowedOrigins)
	check := CheckOrigin(allowed)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !check(r) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", origin)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Key, Authorization")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

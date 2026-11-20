// Package httpserve owns the shared HTTP surface: one listener mounts each
// game's API under a path prefix, serves the single SPA for everything else,
// and reports per-game health.
package httpserve

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// reserved lists the path segments the app owns. Games may not mount API
// routes there, because the router resolves them before any game.
var reserved = []string{"/_app", "/favicon.svg", "/graphql", "/healthz", "/llms.txt", "/robots.txt"}

// Mount declares one game's HTTP surface.
type Mount struct {
	// Game is the game name used in logs and /healthz. Unique.
	Game string
	// Prefix is the absolute path the game is mounted under, e.g. "/mazes".
	Prefix string
	// API lists the paths the game serves under Prefix, e.g. "/graphql".
	// Each entry is matched for the path itself and everything below it.
	API []string
	// Handler serves the game's API. It sees paths with Prefix stripped, so
	// its mux keeps absolute paths like "/graphql".
	Handler http.Handler
	// Health, when non-nil, reports whether the game is healthy. Its error is
	// reported in /healthz and turns the response into 503.
	Health func() error
}

// Router mounts game API surfaces and routes everything else to the SPA.
type Router struct {
	spa http.Handler
	log *log.Logger
	// MaxWebsocketMessageBytes caps one client websocket message per game
	// handler. Zero means DefaultMaxWebsocketMessageBytes.
	MaxWebsocketMessageBytes int64

	mounts []*mount
	routes []route
	// platformRoutes are app-owned handlers outside the game prefixes, such as
	// the unified /graphql surface.
	platformRoutes []platformRoute
	// disabled holds prefixes of compiled-in games excluded by DRBRAIN_GAMES.
	disabled []string
}

// DefaultMaxWebsocketMessageBytes matches the games' GraphQL body cap.
const DefaultMaxWebsocketMessageBytes int64 = 1 << 20

type mount struct {
	Mount
	handler http.Handler
}

type route struct {
	path  string // prefix+api, without a trailing slash
	game  string
	guard *mount
}

type platformRoute struct {
	path    string
	handler http.Handler
}

// NewRouter returns a router that falls back to spa for non-API requests.
func NewRouter(spa http.Handler) *Router {
	return &Router{spa: spa, log: log.Default()}
}

// SetLogger replaces the request logger. A nil logger disables logging.
func (r *Router) SetLogger(l *log.Logger) { r.log = l }

// Mount registers one game. Validation happens here, before the listener binds.
// An error names the offending game so startup can fail fast.
func (r *Router) Mount(m Mount) error {
	if m.Game == "" {
		return errors.New("httpserve: mount with empty game name")
	}
	if m.Handler == nil {
		return fmt.Errorf("httpserve: game %q has nil handler", m.Game)
	}
	if err := validatePrefix(m.Prefix); err != nil {
		return fmt.Errorf("httpserve: game %q: %w", m.Game, err)
	}
	for _, existing := range r.mounts {
		if existing.Prefix == m.Prefix {
			return fmt.Errorf("httpserve: game %q: prefix %q already mounted by %q",
				m.Game, m.Prefix, existing.Game)
		}
	}
	if len(m.API) == 0 {
		// A game may expose no API of its own (delivery: GraphQL moved to the
		// platform root). It still appears in health and its prefix falls
		// through to the SPA.
		r.mounts = append(r.mounts, &mount{Mount: m})
		return nil
	}

	limit := r.MaxWebsocketMessageBytes
	if limit == 0 {
		limit = DefaultMaxWebsocketMessageBytes
	}
	inner := http.StripPrefix(m.Prefix, m.Handler)
	h := r.logRequests(m.Game,
		recoverPanic(r.log, m.Game,
			gzipHandler(
				limitWebsocketMessages(limit, inner))))

	// Validate every API path before touching router state, so a rejected
	// mount leaves the router exactly as it was.
	seen := map[string]bool{}
	var paths []string
	for _, api := range m.API {
		p, err := normalizeAPI(m.Game, m.Prefix, api)
		if err != nil {
			return err
		}
		if seen[p] {
			continue // "/graphql" and "/graphql/" describe the same mount.
		}
		seen[p] = true
		if err := r.checkRouteFree(m.Game, p); err != nil {
			return err
		}
		paths = append(paths, p)
	}
	em := &mount{Mount: m, handler: h}
	for _, p := range paths {
		r.routes = append(r.routes, route{path: p, game: m.Game, guard: em})
	}
	r.mounts = append(r.mounts, em)
	// Longest prefix wins, which keeps "/game/api/nested" ahead of "/game/api".
	sort.SliceStable(r.routes, func(i, j int) bool { return len(r.routes[i].path) > len(r.routes[j].path) })
	return nil
}

// MountPlatform registers an app-owned handler at a root path, outside every
// game prefix. The path serves itself and everything below it. It is wrapped
// with the same gzip, websocket-limit, panic-recovery and request logging as a
// game mount.
func (r *Router) MountPlatform(path string, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("httpserve: platform route %q has nil handler", path)
	}
	if path == "" || path == "/" {
		return fmt.Errorf("httpserve: platform route %q would shadow the app", path)
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return fmt.Errorf("httpserve: platform route %q must be an absolute clean path", path)
	}
	if clean := cleanPath(path); clean != path {
		return fmt.Errorf("httpserve: platform route %q is not clean (want %q)", path, clean)
	}
	for _, rt := range r.routes {
		if rt.path == path || strings.HasPrefix(rt.path, path+"/") || strings.HasPrefix(path, rt.path+"/") {
			return fmt.Errorf("httpserve: platform route %q collides with game %q route %q", path, rt.game, rt.path)
		}
	}
	for _, pr := range r.platformRoutes {
		if pr.path == path {
			return fmt.Errorf("httpserve: platform route %q already mounted", path)
		}
	}

	limit := r.MaxWebsocketMessageBytes
	if limit == 0 {
		limit = DefaultMaxWebsocketMessageBytes
	}
	h := r.logRequests("platform",
		recoverPanic(r.log, "platform",
			gzipHandler(
				limitWebsocketMessages(limit, handler))))
	r.platformRoutes = append(r.platformRoutes, platformRoute{path: path, handler: h})
	sort.SliceStable(r.platformRoutes, func(i, j int) bool { return len(r.platformRoutes[i].path) > len(r.platformRoutes[j].path) })
	return nil
}

// Disable reserves a compiled-in game's whole prefix with 404 responses. Games
// left out of DRBRAIN_GAMES must fail loudly instead of serving the SPA shell or
// an empty API, and must not appear in /healthz.
func (r *Router) Disable(game, prefix string) error {
	if err := validatePrefix(prefix); err != nil {
		return fmt.Errorf("httpserve: game %q: %w", game, err)
	}
	for _, p := range r.disabled {
		if p == prefix {
			return nil
		}
	}
	for _, m := range r.mounts {
		if m.Prefix == prefix {
			return fmt.Errorf("httpserve: game %q: cannot disable mounted prefix %q", game, prefix)
		}
	}
	r.disabled = append(r.disabled, prefix)
	return nil
}

// disabledPath reports whether path belongs to a disabled game.
func (r *Router) disabledPath(path string) bool {
	for _, p := range r.disabled {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return errors.New("empty prefix")
	}
	if prefix == "/" {
		return errors.New("prefix \"/\" would shadow the app")
	}
	if !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("prefix %q must be absolute", prefix)
	}
	if strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("prefix %q must not end in a slash", prefix)
	}
	if strings.ContainsAny(prefix, "?#") {
		return fmt.Errorf("prefix %q must not contain a query or fragment", prefix)
	}
	if p := cleanPath(prefix); p != prefix {
		return fmt.Errorf("prefix %q is not a clean path (want %q)", prefix, p)
	}
	if isReserved(prefix) {
		return fmt.Errorf("prefix %q is reserved by the app", prefix)
	}
	return nil
}

// normalizeAPI validates one API path and returns its full, slash-trimmed path.
func normalizeAPI(game, prefix, api string) (string, error) {
	if api == "" {
		return "", fmt.Errorf("httpserve: game %q: empty API path", game)
	}
	if api == "/" {
		return "", fmt.Errorf("httpserve: game %q: API path \"/\" is a catch-all under %q", game, prefix)
	}
	if !strings.HasPrefix(api, "/") {
		return "", fmt.Errorf("httpserve: game %q: API path %q must be absolute", game, api)
	}
	if strings.ContainsAny(api, "?#") {
		return "", fmt.Errorf("httpserve: game %q: API path %q must not contain a query or fragment", game, api)
	}
	trimmed := strings.TrimSuffix(api, "/")
	clean := cleanPath(trimmed)
	if clean != trimmed {
		return "", fmt.Errorf("httpserve: game %q: API path %q is not a clean path (want %q)", game, api, clean)
	}
	full := prefix + trimmed
	if isReserved(full) {
		return "", fmt.Errorf("httpserve: game %q: API path %q collides with a reserved app path", game, full)
	}
	return full, nil
}

func (r *Router) checkRouteFree(game, path string) error {
	for _, existing := range r.routes {
		if existing.path == path {
			return fmt.Errorf("httpserve: game %q: API path %q already mounted by %q",
				game, path, existing.game)
		}
	}
	return nil
}

// isReserved reports whether path is, or lies under, an app-owned segment.
func isReserved(path string) bool {
	for _, r := range reserved {
		if path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}

func cleanPath(p string) string {
	return path.Clean(p)
}

// Handler returns the composed handler.
func (r *Router) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if path == "/healthz" {
			r.serveHealth(w)
			return
		}
		if h := r.matchPlatform(path); h != nil {
			h.ServeHTTP(w, req)
			return
		}
		if r.disabledPath(path) {
			http.NotFound(w, req)
			return
		}
		if m := r.match(path); m != nil {
			m.handler.ServeHTTP(w, req)
			return
		}
		r.spa.ServeHTTP(w, req)
	})
}

// match returns the mount serving path, if any. A declared API path serves
// itself and everything below it; it never matches the bare prefix.
func (r *Router) match(path string) *mount {
	for _, rt := range r.routes {
		if path == rt.path || strings.HasPrefix(path, rt.path+"/") {
			return rt.guard
		}
	}
	return nil
}

// matchPlatform returns the app-owned handler for path, if any.
func (r *Router) matchPlatform(path string) http.Handler {
	for _, rt := range r.platformRoutes {
		if path == rt.path || strings.HasPrefix(path, rt.path+"/") {
			return rt.handler
		}
	}
	return nil
}

// serveHealth writes one JSON object with per-game status. Any unhealthy game
// makes the whole response 503.
type healthResponse struct {
	Status string            `json:"status"`
	Games  map[string]string `json:"games"`
}

func (r *Router) serveHealth(w http.ResponseWriter) {
	resp := healthResponse{Status: "ok", Games: make(map[string]string, len(r.mounts))}
	unhealthy := false
	names := make([]string, 0, len(r.mounts))
	for _, m := range r.mounts {
		names = append(names, m.Game)
	}
	sort.Strings(names)
	byName := make(map[string]*mount, len(r.mounts))
	for _, m := range r.mounts {
		byName[m.Game] = m
	}
	for _, name := range names {
		m := byName[name]
		status := "ok"
		if m.Health != nil {
			if err := m.Health(); err != nil {
				status = "unhealthy: " + err.Error()
				unhealthy = true
			}
		}
		resp.Games[name] = status
	}
	if unhealthy {
		resp.Status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if unhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// logRequests logs one line per request with the owning game.
func (r *Router) logRequests(game string, next http.Handler) http.Handler {
	if r.log == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, req)
		r.log.Printf("%s %s %s %d %s", game, req.Method, req.URL.RequestURI(), sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// statusWriter records the response status without changing the body.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(p)
}

// Unwrap keeps http.ResponseController working through the wrapper.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Hijack lets a websocket upgrade through the logger unchanged.
func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpserve: response does not implement http.Hijacker")
	}
	conn, brw, err := hj.Hijack()
	if err == nil {
		s.status = http.StatusSwitchingProtocols
		s.wroteHeader = true
	}
	return conn, brw, err
}

// Flush lets SSE handlers flush through the logger.
func (s *statusWriter) Flush() {
	if fl, ok := s.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// recoverPanic turns a game handler panic into a logged 500 without taking the
// process (or the other games) down.
func recoverPanic(l *log.Logger, game string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if l != nil {
					l.Printf("%s panic serving %s: %v", game, req.URL.RequestURI(), v)
				}
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, req)
	})
}

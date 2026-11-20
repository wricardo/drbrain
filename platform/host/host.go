// Package host owns the process lifecycle: it mounts enabled games before the
// listener binds, serves them on one port, and fans shutdown out in parallel.
package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/httpserve"
)

// ReadHeaderTimeout bounds the request-header read, matching the games'
// existing servers.
const ReadHeaderTimeout = 10 * time.Second

// GraphQLBindings is what enabled games hand the unified GraphQL builder. The
// values stay opaque so platform does not import game packages.
type GraphQLBindings struct {
	Resolvers map[string]any
	// Accounts and Authn are the global services every namespace may use.
	Accounts *account.Service
	Authn    *auth.Auth
}

// GraphQLBuilder constructs the single /graphql handler from the mounted
// games' resolver roots. cmd/drbrain supplies it because only that package may
// import every game and the unified graph package.
type GraphQLBuilder func(GraphQLBindings) (http.Handler, error)

// Option configures a Host.
type Option func(*Host)

// WithGraphQL installs the unified GraphQL builder. Without it, the host is
// API-only for the per-game surfaces.
func WithGraphQL(b GraphQLBuilder) Option {
	return func(h *Host) { h.graphQL = b }
}

// WithServices installs the global account and auth services handed to games.
func WithServices(accounts *account.Service, authn *auth.Auth) Option {
	return func(h *Host) {
		h.accounts = accounts
		h.auth = authn
	}
}

// WithDB installs the shared PostgreSQL pool handed to games.
func WithDB(pool *pgxpool.Pool) Option {
	return func(h *Host) { h.db = pool }
}

// Host is one process serving the enabled games.
type Host struct {
	cfg      config.Config
	reg      *Registry
	logger   *slog.Logger
	graphQL  GraphQLBuilder
	accounts *account.Service
	auth     *auth.Auth
	db       *pgxpool.Pool

	ready chan struct{}
	ln    net.Listener
	srv   *http.Server

	shutdownOnce sync.Once
	shutdownErr  error
}

// New builds a host. No socket is opened until Run.
func New(cfg config.Config, reg *Registry, logger *slog.Logger, opts ...Option) *Host {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Host{cfg: cfg, reg: reg, logger: logger, ready: make(chan struct{})}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Ready is closed once the listener has bound and the games are mounted.
func (h *Host) Ready() <-chan struct{} { return h.ready }

// Addr is the bound address. Only valid after Ready.
func (h *Host) Addr() string {
	if h.ln == nil {
		return ""
	}
	return h.ln.Addr().String()
}

// Run validates the selection, mounts every enabled game, binds, and serves
// until ctx is canceled or serving fails. Cancellation drains HTTP first, then
// calls each game's Shutdown once, in parallel.
func (h *Host) Run(ctx context.Context) error {
	enabled, err := h.reg.Select(h.cfg.EnabledGames())
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		return errors.New("no games enabled")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	handler, err := h.mount(enabled)
	if err != nil {
		return err
	}

	// Everything that can fail fast has failed by now. Bind before logging
	// "listening", so a held port is the first thing the operator sees.
	ln, err := net.Listen("tcp", h.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", h.cfg.Addr, err)
	}
	h.ln = ln
	h.srv = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
	}
	close(h.ready)

	names := make([]string, 0, len(enabled))
	for _, g := range enabled {
		names = append(names, g.Name())
	}
	h.logger.Info("listening", "addr", ln.Addr().String(), "games", names)

	serveErr := make(chan error, 1)
	go func() { serveErr <- h.srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return h.shutdownGames(enabled)
	case <-ctx.Done():
		h.logger.Info("shutting down")
	}

	var httpErr error
	shutdownCtx, cancel := context.WithTimeout(context.Background(), h.cfg.ShutdownTimeout)
	if err := h.srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		httpErr = fmt.Errorf("http shutdown: %w", err)
	}
	cancel()

	return errors.Join(httpErr, h.shutdownGames(enabled))
}

// mount builds the router and lets each game register its handlers. A game
// error aborts before any socket exists.
func (h *Host) mount(enabled []platform.Game) (http.Handler, error) {
	if _, err := os.Stat(h.cfg.StaticDir); err != nil {
		h.logger.Warn("no web app; API-only", "static_dir", h.cfg.StaticDir, "err", err)
	}
	router := httpserve.NewRouter(httpserve.SPAHandler(h.cfg.StaticDir))
	bindings := GraphQLBindings{
		Resolvers: make(map[string]any, len(enabled)),
		Accounts:  h.accounts,
		Authn:     h.auth,
	}
	for _, g := range enabled {
		mux := http.NewServeMux()
		deps := platform.Deps{
			Config:    h.cfg,
			StaticDir: h.cfg.StaticDir,
			PublicURL: h.cfg.PublicURL,
			Logger:    h.logger.With("game", g.Name()),
			DB:        h.db,
			Accounts:  h.accounts,
			Auth:      h.auth,
		}
		if err := g.Mount(mux, deps); err != nil {
			return nil, fmt.Errorf("game %s: mount: %w", g.Name(), err)
		}
		if gr, ok := g.(platform.GraphQLResolver); ok {
			if resolver := gr.GraphQLResolver(); resolver != nil {
				bindings.Resolvers[g.Name()] = resolver
			}
		}
		api := append([]string(nil), g.API()...)
		if lp, ok := g.(platform.LLMSProvider); ok {
			tmpl, err := template.New(g.Name() + "-llms").Parse(lp.LLMSTemplate())
			if err != nil {
				return nil, fmt.Errorf("game %s: llms template: %w", g.Name(), err)
			}
			publicURL := lp.LLMSPublicURL()
			if publicURL == "" {
				publicURL = h.cfg.PublicURL
			}
			mux.Handle("/llms.txt", llmsHandler(tmpl, g.Prefix(), publicURL))
			api = append(api, "/llms.txt")
		}
		if err := router.Mount(httpserve.Mount{
			Game:    g.Name(),
			Prefix:  g.Prefix(),
			API:     api,
			Handler: mux,
		}); err != nil {
			return nil, err
		}
	}
	enabledNames := make(map[string]bool, len(enabled))
	for _, g := range enabled {
		enabledNames[g.Name()] = true
	}
	for _, g := range h.reg.Games() {
		if enabledNames[g.Name()] {
			continue
		}
		if err := router.Disable(g.Name(), g.Prefix()); err != nil {
			return nil, err
		}
	}
	if h.auth != nil {
		if err := router.MountPlatform("/auth", h.auth.Handler()); err != nil {
			return nil, fmt.Errorf("host: mount auth: %w", err)
		}
	}
	if h.graphQL != nil {
		if len(bindings.Resolvers) == 0 {
			return nil, errors.New("host: no game exposes a GraphQL resolver")
		}
		handler, err := h.graphQL(bindings)
		if err != nil {
			return nil, fmt.Errorf("host: unified graphql: %w", err)
		}
		if err := router.MountPlatform("/graphql", handler); err != nil {
			return nil, err
		}
	}
	return router.Handler(), nil
}

// shutdownGames runs every enabled game's Shutdown exactly once, in parallel,
// each bounded by the configured timeout. Every game gets its chance to save
// even if another fails.
func (h *Host) shutdownGames(enabled []platform.Game) error {
	h.shutdownOnce.Do(func() {
		var (
			mu   sync.Mutex
			errs []error
			wg   sync.WaitGroup
		)
		for _, g := range enabled {
			wg.Add(1)
			go func(g platform.Game) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), h.cfg.ShutdownTimeout)
				defer cancel()
				if err := g.Shutdown(ctx); err != nil {
					wrapped := fmt.Errorf("game %s: save on shutdown: %w", g.Name(), err)
					h.logger.Error("shutdown failed", "game", g.Name(), "err", err)
					mu.Lock()
					errs = append(errs, wrapped)
					mu.Unlock()
				}
			}(g)
		}
		wg.Wait()
		h.shutdownErr = errors.Join(errs...)
	})
	return h.shutdownErr
}

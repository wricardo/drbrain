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

	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/httpserve"
)

// ReadHeaderTimeout bounds the request-header read, matching the games'
// existing servers.
const ReadHeaderTimeout = 10 * time.Second

// Host is one process serving the enabled games.
type Host struct {
	cfg    config.Config
	reg    *Registry
	logger *slog.Logger

	ready chan struct{}
	ln    net.Listener
	srv   *http.Server

	shutdownOnce sync.Once
	shutdownErr  error
}

// New builds a host. No socket is opened until Run.
func New(cfg config.Config, reg *Registry, logger *slog.Logger) *Host {
	if logger == nil {
		logger = slog.Default()
	}
	return &Host{cfg: cfg, reg: reg, logger: logger, ready: make(chan struct{})}
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
	for _, g := range enabled {
		mux := http.NewServeMux()
		deps := platform.Deps{
			Config:    h.cfg,
			StaticDir: h.cfg.StaticDir,
			PublicURL: h.cfg.PublicURL,
			Logger:    h.logger.With("game", g.Name()),
		}
		if err := g.Mount(mux, deps); err != nil {
			return nil, fmt.Errorf("game %s: mount: %w", g.Name(), err)
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

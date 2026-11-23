//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

// Package mazes is Dr Brain - Mazes as a platform game: a tower-of-levels step
// maze where a player sees only the level they stand on.
package mazes

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	mazeconfig "github.com/wricardo/drbrain/games/mazes/game/config"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/game/session"
	"github.com/wricardo/drbrain/games/mazes/graph"
	"github.com/wricardo/drbrain/games/mazes/transport/websocket"
	"github.com/wricardo/drbrain/platform"
)

// llmsTemplate is the agent guide. The platform renders it at
// /mazes/llms.txt; the game only supplies the template and its base URL.
//
//go:embed llms.txt.tmpl
var llmsTemplate string

// Env is this game's environment section: MAZES_*.
type Env struct {
	MapsDir      string `envconfig:"MAPS_DIR" default:"maps"`
	SolutionsDir string `envconfig:"SOLUTIONS_DIR" default:"solutions"`
	PublicURL    string `envconfig:"PUBLIC_URL"`
	Debug        bool   `envconfig:"DEBUG"`
	MaxSessions  int    `envconfig:"MAX_SESSIONS" default:"1000"`
	// MaxUserMaps caps player-created maps (all accounts; 0 = no cap).
	MaxUserMaps int `envconfig:"MAX_USER_MAPS" default:"200"`
}

func loadEnv() (Env, error) {
	var e Env
	if err := envconfig.Process("MAZES", &e); err != nil {
		return Env{}, err
	}
	return e, nil
}

// Game is Dr Brain - Mazes as a platform game. New is cheap and cannot fail;
// maps and sessions are loaded in Mount, before the host binds.
type Game struct {
	env       Env
	svc       service.GameService
	sessions  *session.Manager
	hub       *websocket.Hub
	resolver  *graph.Resolver
	publicURL string
	// hooksRegistered guards the account-deletion hook: Mount runs once, but a
	// second Mount must not double-register it.
	hooksRegistered bool
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "mazes" }
func (g *Game) Prefix() string { return "/mazes" }
func (g *Game) API() []string  { return nil }

// GraphQLResolver implements platform.GraphQLResolver.
func (g *Game) GraphQLResolver() any { return g.resolver }

// LLMSTemplate implements platform.LLMSProvider.
func (g *Game) LLMSTemplate() string { return llmsTemplate }

// LLMSPublicURL implements platform.LLMSProvider.
func (g *Game) LLMSPublicURL() string { return g.publicURL }

// Mount wires the game exactly as the old standalone main did, then registers
// only its API routes: no listener, no SPA, no /healthz, no signals.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	env, err := loadEnv()
	if err != nil {
		return fmt.Errorf("MAZES_ environment: %w", err)
	}
	g.env = env
	g.publicURL = env.PublicURL

	if deps.DB == nil {
		return fmt.Errorf("session persistence: PostgreSQL is required")
	}
	maps, err := mazeconfig.NewManager(mazeconfig.Options{
		Dir:         env.MapsDir,
		Pool:        deps.DB,
		MaxUserMaps: env.MaxUserMaps,
	})
	if err != nil {
		return fmt.Errorf("load maps: %w", err)
	}
	if deps.Accounts != nil && !g.hooksRegistered {
		g.hooksRegistered = true
		// Delete the account's drafts, keep its published maps playable.
		deps.Accounts.AddOnDelete(func(accountID string) error {
			return errors.Join(
				maps.DeleteDraftsByOwner(accountID),
				maps.OrphanPublishedByOwner(accountID),
			)
		})
	}
	persistence, err := session.NewPostgresPersistence(deps.DB, env.MaxSessions)
	if err != nil {
		return fmt.Errorf("session persistence: %w", err)
	}
	sessions := session.NewManager(persistence, env.MaxSessions)
	if err := sessions.LoadAll(); err != nil {
		deps.Logger.Warn("loading persisted sessions", "err", err)
	}

	hub := websocket.NewHub()
	svc := service.New(maps, sessions, hub, service.Options{
		SessionTTL: envDuration("SESSION_TTL", 7*24*time.Hour),
	})

	g.svc = svc
	g.sessions = sessions
	g.hub = hub
	g.resolver = graph.NewResolver(svc, hub)
	return nil
}

// Shutdown persists every live session, matching the old binary's SIGTERM
// path. The service logs individual save failures and is idempotent.
func (g *Game) Shutdown(context.Context) error {
	if g.svc != nil {
		g.svc.Shutdown()
	}
	return nil
}

// envDuration reads a duration env var; unset or invalid returns def.
func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return def
	}
	return d
}

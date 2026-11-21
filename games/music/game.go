//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

// Package music is Dr Brain - Music Region as a platform game: restore a
// scrambled classical excerpt by reordering its measures and undoing flips.
package music

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/game/session"
	"github.com/wricardo/drbrain/games/music/graph"
	"github.com/wricardo/drbrain/games/music/transport/websocket"
	"github.com/wricardo/drbrain/platform"
)

// llmsTemplate is the agent guide. The platform renders it at
// /music/llms.txt; the game only supplies the template and its base URL.
//
//go:embed llms.txt.tmpl
var llmsTemplate string

// Env is this game's environment section: MUSIC_*.
type Env struct {
	ScoresDir       string        `envconfig:"SCORES_DIR" default:"scores"`
	CustomScoresDir string        `envconfig:"CUSTOM_SCORES_DIR"`
	SessionsDir     string        `envconfig:"SESSIONS_DIR" default:"sessions"`
	MaxSessions     int           `envconfig:"MAX_SESSIONS" default:"1000"`
	SessionTTL      time.Duration `envconfig:"SESSION_TTL" default:"168h"`
	PublicURL       string        `envconfig:"PUBLIC_URL"`
	Debug           bool          `envconfig:"DEBUG"`
}

func loadEnv() (Env, error) {
	var e Env
	if err := envconfig.Process("MUSIC", &e); err != nil {
		return Env{}, err
	}
	return e, nil
}

// Game is Dr Brain - Music Region as a platform game. New is cheap and cannot
// fail; scores and sessions are loaded in Mount, before the host binds.
type Game struct {
	env       Env
	svc       service.GameService
	sessions  *session.Manager
	hub       *websocket.Hub
	resolver  *graph.Resolver
	publicURL string
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "music" }
func (g *Game) Prefix() string { return "/music" }
func (g *Game) API() []string  { return nil }

// GraphQLResolver implements platform.GraphQLResolver.
func (g *Game) GraphQLResolver() any { return g.resolver }

// LLMSTemplate implements platform.LLMSProvider.
func (g *Game) LLMSTemplate() string { return llmsTemplate }

// LLMSPublicURL implements platform.LLMSProvider.
func (g *Game) LLMSPublicURL() string { return g.publicURL }

// Mount loads the score catalogue and persisted sessions, then wires the
// service and resolver. Both run before the listener binds, so a bad score or
// session file aborts startup with the game named.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	env, err := loadEnv()
	if err != nil {
		return fmt.Errorf("MUSIC_ environment: %w", err)
	}
	g.env = env
	g.publicURL = env.PublicURL

	scores, err := score.NewCatalogue(env.ScoresDir, env.CustomScoresDir)
	if err != nil {
		return fmt.Errorf("load scores: %w", err)
	}
	persistence, err := session.NewStorePersistence(env.SessionsDir, env.MaxSessions)
	if err != nil {
		return fmt.Errorf("session persistence: %w", err)
	}
	sessions := session.NewManager(persistence, env.MaxSessions)
	if err := sessions.LoadAll(); err != nil && deps.Logger != nil {
		deps.Logger.Warn("loading persisted music sessions", "err", err)
	}

	hub := websocket.NewHub()
	svc := service.New(scores, sessions, hub, service.Options{SessionTTL: env.SessionTTL})

	g.svc = svc
	g.sessions = sessions
	g.hub = hub
	g.resolver = graph.NewResolver(svc, hub)
	return nil
}

// Shutdown persists every live session and closes the subscription hub. The
// service logs individual save failures and is idempotent.
func (g *Game) Shutdown(context.Context) error {
	if g.svc != nil {
		g.svc.Shutdown()
	}
	if g.hub != nil {
		g.hub.Close()
	}
	return nil
}

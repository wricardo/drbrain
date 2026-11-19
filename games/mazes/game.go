package mazes

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/wricardo/drbrain/games/mazes/api"
	mazeconfig "github.com/wricardo/drbrain/games/mazes/game/config"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/game/session"
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
	SessionsDir  string `envconfig:"SESSIONS_DIR" default:"sessions"`
	SolutionsDir string `envconfig:"SOLUTIONS_DIR" default:"solutions"`
	PublicURL    string `envconfig:"PUBLIC_URL"`
	Debug        bool   `envconfig:"DEBUG"`
	MaxSessions  int    `envconfig:"MAX_SESSIONS" default:"1000"`
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
	publicURL string
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "mazes" }
func (g *Game) Prefix() string { return "/mazes" }
func (g *Game) API() []string  { return []string{"/graphql", "/graphql/"} }

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

	maps, err := mazeconfig.NewManager(env.MapsDir)
	if err != nil {
		return fmt.Errorf("load maps: %w", err)
	}
	persistence, err := session.NewStorePersistence(env.SessionsDir, env.MaxSessions)
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

	opts := api.Options{
		Introspection:  envBool("GRAPHQL_INTROSPECTION", true),
		Playground:     envBool("GRAPHQL_PLAYGROUND", true),
		AllowedOrigins: os.Getenv("ALLOWED_ORIGINS"),
		PublicURL:      env.PublicURL,
		LLMSTemplate:   llmsTemplate,
	}
	deps.Logger.Debug("graphql settings",
		"introspection", opts.Introspection,
		"playground", opts.Playground,
		"maps_dir", env.MapsDir,
		"sessions_dir", env.SessionsDir)

	gql := api.NewGraphQLHandler(svc, hub, opts)
	mux.Handle("/graphql", gql)
	mux.Handle("/graphql/", gql)
	mux.Handle("/graphql/playground", api.NewPlaygroundHandler("/mazes/graphql"))

	g.svc = svc
	g.sessions = sessions
	g.hub = hub
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

// envBool reads a boolean env var; unset returns def.
func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "":
		return def
	case "true", "1", "yes":
		return true
	default:
		return false
	}
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

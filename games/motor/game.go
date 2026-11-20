//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

package motor

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"

	"github.com/kelseyhightower/envconfig"
	motorconfig "github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/games/motor/graph"
	"github.com/wricardo/drbrain/games/motor/internal/appconfig"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
	"github.com/wricardo/drbrain/platform"
)

// prefix is the public mount path. Cookies, OAuth callbacks and the playground
// all use it; the routes themselves stay relative inside the game.
const prefix = "/motor"

// llmsTemplate is the agent guide, rendered by the platform at /motor/llms.txt.
//
//go:embed llms.txt.tmpl
var llmsTemplate string

// Env is this game's environment section: MOTOR_*.
type Env struct {
	MapsDir      string `envconfig:"MAPS_DIR" default:"maps"`
	UserMapsDir  string `envconfig:"USER_MAPS_DIR" default:"maps-user"`
	SessionsDir  string `envconfig:"SESSIONS_DIR" default:"sessions"`
	SolutionsDir string `envconfig:"SOLUTIONS_DIR" default:"solutions"`
	PublicURL    string `envconfig:"PUBLIC_URL"`
	Debug        bool   `envconfig:"DEBUG"`
	// MaxSessions zero means "use the existing MAX_SESSIONS setting".
	MaxSessions int `envconfig:"MAX_SESSIONS"`
}

func loadEnv() (Env, error) {
	var e Env
	if err := envconfig.Process("MOTOR", &e); err != nil {
		return Env{}, err
	}
	return e, nil
}

// Game is Dr Brain - Motor Programming on the platform. Accounts, auth and the
// PostgreSQL pool come from the platform; the game owns sessions and maps.
type Game struct {
	env       Env
	publicURL string
	svc       service.GameService
	resolver  *graph.Resolver
}

// New returns the game for the compiled-in registry. Work happens in Mount so
// a bad map or database URL fails before the listener binds.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "motor" }
func (g *Game) Prefix() string { return prefix }
func (g *Game) API() []string  { return nil }

// GraphQLResolver implements platform.GraphQLResolver.
func (g *Game) GraphQLResolver() any { return g.resolver }

// LLMSTemplate implements platform.LLMSProvider.
func (g *Game) LLMSTemplate() string { return llmsTemplate }

// LLMSPublicURL implements platform.LLMSProvider.
func (g *Game) LLMSPublicURL() string { return g.publicURL }

// Mount wires storage, sign-in and GraphQL exactly as the old main did, then
// registers the API routes. No listener, SPA, /healthz or signals.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	env, err := loadEnv()
	if err != nil {
		return fmt.Errorf("MOTOR_ environment: %w", err)
	}
	g.env = env
	g.publicURL = env.PublicURL
	if g.publicURL == "" {
		g.publicURL = deps.Config.PublicURL
	}

	cfg, err := appconfig.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	// Storage: the platform pool is shared with the global account service. It
	// is nil when file storage is configured.
	pool := deps.DB

	maxSessions := env.MaxSessions
	if maxSessions <= 0 {
		maxSessions = cfg.MaxSessions.Int()
	}
	mapOpts := motorconfig.Options{
		Dir:         env.MapsDir,
		UserDir:     env.UserMapsDir,
		MaxUserMaps: cfg.MaxUserMaps.Int(),
	}
	var persistence session.SessionPersistence
	if pool != nil {
		mapOpts.UserDir = ""
		mapOpts.UserStore = motorconfig.NewPostgresUserMaps(pool)
		persistence = session.NewPostgresPersistence(pool)
	} else {
		fp, err := session.NewStorePersistence(env.SessionsDir, maxSessions)
		if err != nil {
			return fmt.Errorf("session persistence: %w", err)
		}
		persistence = fp
	}
	maps, err := motorconfig.NewManager(mapOpts)
	if err != nil {
		return fmt.Errorf("load maps: %w", err)
	}
	sessions := session.NewManager(persistence, maxSessions)
	if err := sessions.LoadAll(); err != nil {
		deps.Logger.Warn("loading persisted sessions", "err", err)
	}

	hub := websocket.NewHub()
	svc := service.New(maps, sessions, hub, service.Options{
		SessionTTL:    cfg.SessionTTL.Duration(),
		SweepInterval: cfg.SessionSweepInterval.Duration(),
		Accounts:      deps.Accounts,
	})
	if deps.Accounts != nil {
		deps.Accounts.SetOnDelete(svc.DetachAccount)
	}

	resolver := graph.NewResolver(svc, hub)
	resolver.Accounts = deps.Accounts
	resolver.Admin = graph.Admin{
		Key:                  cfg.AdminKey,
		AllowUnauthenticated: cfg.AllowUnauthenticatedAdmin.Bool(),
	}
	g.resolver = resolver

	g.svc = svc
	return nil
}

// Shutdown persists every session. The shared pool is closed by the process.
func (g *Game) Shutdown(context.Context) error {
	if g.svc != nil {
		g.svc.Shutdown()
	}
	return nil
}

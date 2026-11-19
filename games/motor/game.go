package motor

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kelseyhightower/envconfig"
	"github.com/wricardo/drbrain/games/motor/api"
	"github.com/wricardo/drbrain/games/motor/auth"
	"github.com/wricardo/drbrain/games/motor/game/account"
	motorconfig "github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/games/motor/internal/appconfig"
	"github.com/wricardo/drbrain/games/motor/internal/pgdb"
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
	AccountsDir  string `envconfig:"ACCOUNTS_DIR" default:"accounts"`
	SolutionsDir string `envconfig:"SOLUTIONS_DIR" default:"solutions"`
	DatabaseURL  string `envconfig:"DATABASE_URL"`
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

// Game is Dr Brain - Motor Programming on the platform. Postgres and the
// account code stay here; the host only owns the listener and lifecycle.
type Game struct {
	env       Env
	publicURL string
	svc       service.GameService
	pool      *pgxpool.Pool
}

// New returns the game for the compiled-in registry. Work happens in Mount so
// a bad map or database URL fails before the listener binds.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "motor" }
func (g *Game) Prefix() string { return prefix }
func (g *Game) API() []string  { return []string{"/graphql", "/graphql/", "/auth/"} }

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

	// Storage: with a database URL, sessions, accounts, logins and player maps
	// live in PostgreSQL; without one they stay JSON files (the rollback path).
	// A configured but unreachable database is a startup error, never a silent
	// fallback to files.
	dbURL := strings.TrimSpace(env.DatabaseURL)
	if dbURL == "" {
		dbURL = cfg.DatabaseURL
	}
	if dbURL != "" {
		pool, err := pgdb.Open(context.Background(), dbURL)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		g.pool = pool
	}

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
	if g.pool != nil {
		mapOpts.UserDir = ""
		mapOpts.UserStore = motorconfig.NewPostgresUserMaps(g.pool)
		persistence = session.NewPostgresPersistence(g.pool)
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

	// Sign-in: username/password accounts are on by default; OAuth providers are
	// each on when both their id and secret are set. With neither, nothing is
	// created and the server behaves as before.
	providers, err := auth.Providers(auth.ProvidersConfig{
		GoogleID:     cfg.AuthGoogleClientID,
		GoogleSecret: cfg.AuthGoogleClientSecret,
		GitHubID:     cfg.AuthGitHubClientID,
		GitHubSecret: cfg.AuthGitHubClientSecret,
		DevFake:      cfg.AuthDevFake.Bool(),
		BasePath:     prefix,
	}, g.publicURL)
	if err != nil {
		return fmt.Errorf("sign-in configuration: %w", err)
	}
	passwordSignIn := cfg.AuthPassword.Bool()
	var accounts service.AccountStore
	switch {
	case g.pool != nil:
		if len(providers) > 0 || passwordSignIn {
			accounts = account.NewPostgresStore(g.pool)
		}
	case len(providers) > 0 || passwordSignIn || dirExists(env.AccountsDir):
		fileAccounts, err := account.Open(env.AccountsDir, nil)
		if err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
		accounts = fileAccounts
	}

	hub := websocket.NewHub()
	svcOpts := service.Options{
		SessionTTL:     cfg.SessionTTL.Duration(),
		SweepInterval:  cfg.SessionSweepInterval.Duration(),
		MaxAccounts:    cfg.MaxAccounts.Int(),
		PasswordSignIn: passwordSignIn,
	}
	if accounts != nil {
		svcOpts.Accounts = accounts
	}
	svc := service.New(maps, sessions, hub, svcOpts)

	var authHandler *auth.Auth
	if len(providers) > 0 || passwordSignIn {
		authHandler, err = auth.New(auth.Config{
			Providers: providers, Accounts: svc, PublicURL: g.publicURL, AllowedOrigins: cfg.AllowedOrigins,
			Password: passwordSignIn, BasePath: prefix,
		})
		if err != nil {
			return fmt.Errorf("sign-in: %w", err)
		}
		if passwordSignIn && g.publicURL == "" {
			deps.Logger.Warn("no MOTOR_PUBLIC_URL: sign-in is accepted only on localhost hosts or ALLOWED_ORIGINS")
		}
	}

	opts := api.Options{
		Introspection:             cfg.GraphQLIntrospection.Bool(),
		Playground:                cfg.GraphQLPlayground.Bool(),
		AllowedOrigins:            cfg.AllowedOrigins,
		PublicURL:                 g.publicURL,
		AdminKey:                  cfg.AdminKey,
		AllowUnauthenticatedAdmin: cfg.AllowUnauthenticatedAdmin.Bool(),
		LLMSTemplate:              llmsTemplate,
		Auth:                      authHandler,
	}
	deps.Logger.Debug("graphql settings",
		"introspection", opts.Introspection,
		"playground", opts.Playground,
		"password_sign_in", passwordSignIn,
		"postgres", g.pool != nil,
		"maps_dir", env.MapsDir,
		"sessions_dir", env.SessionsDir)

	gql := api.NewGraphQLHandler(svc, hub, opts)
	mux.Handle("/graphql", gql)
	mux.Handle("/graphql/", gql)
	mux.Handle("/graphql/playground", api.NewPlaygroundHandler(prefix+"/graphql"))
	if authHandler != nil {
		mux.Handle("/auth/", api.NewAuthRoutes(authHandler))
	}

	g.svc = svc
	return nil
}

// Shutdown persists every session, then closes the database pool. The service
// logs individual save failures and is idempotent.
func (g *Game) Shutdown(context.Context) error {
	if g.svc != nil {
		g.svc.Shutdown()
	}
	if g.pool != nil {
		g.pool.Close()
	}
	return nil
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

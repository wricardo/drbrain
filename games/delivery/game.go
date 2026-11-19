package delivery

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kelseyhightower/envconfig"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph"
	"github.com/wricardo/drbrain/platform"
)

const prefix = "/delivery"

// Env is this game's environment section: DELIVERY_*.
type Env struct {
	// City is the hand-authored city file.
	City string `envconfig:"CITY" default:"maps/city.json"`
	// SessionsDir holds saved sessions; listing includes files not in memory.
	SessionsDir string `envconfig:"SESSIONS_DIR" default:"sessions"`
}

func loadEnv() (Env, error) {
	var e Env
	if err := envconfig.Process("DELIVERY", &e); err != nil {
		return Env{}, err
	}
	return e, nil
}

// Game is DrBrain Delivery on the platform. The tick loop stays inside each
// session; the host only owns the listener and lifecycle.
type Game struct {
	env Env
	mgr *session.Manager
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "delivery" }
func (g *Game) Prefix() string { return prefix }
func (g *Game) API() []string  { return []string{"/graphql", "/graphql/"} }

// Mount validates the city and builds the manager before the host binds, then
// registers only the API routes. Sessions are loaded on demand; List() already
// reports saves that are not in memory with loaded=false.
//
// The old standalone server's startup discipline is preserved: a bad city file
// fails the process (the host wraps this error with the game name) before any
// port is opened.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	env, err := loadEnv()
	if err != nil {
		return fmt.Errorf("DELIVERY_ environment: %w", err)
	}
	g.env = env

	// Parse the city now, not on first Create: an operator must see a broken
	// map before the listener exists, not as a failed request later.
	if _, _, err := engine.CityFromFile(env.City); err != nil {
		return fmt.Errorf("city %q: %w", env.City, err)
	}
	mgr, err := session.NewManager(env.City, env.SessionsDir)
	if err != nil {
		return fmt.Errorf("session manager: %w", err)
	}
	g.mgr = mgr

	// The platform host gzips and limits the mounted surface; the game's own
	// Handler would compress a second time.
	gql := graph.UncompressedHandler(mgr)
	mux.Handle("/graphql", gql)
	mux.Handle("/graphql/", gql)
	mux.Handle("/graphql/playground", graph.PlaygroundAt(prefix+"/graphql"))

	deps.Logger.Info("delivery mounted", "city", env.City, "sessions_dir", env.SessionsDir)
	return nil
}

// Shutdown saves every session (Manager.Close → SaveAll) so a restart reloads
// paused with the same ledger and progress. Its error is the host's non-zero
// exit.
func (g *Game) Shutdown(context.Context) error {
	if g.mgr == nil {
		return nil
	}
	return g.mgr.Close()
}

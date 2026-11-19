package platform

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/wricardo/drbrain/platform/config"
)

// Config is the platform configuration games receive.
type Config = config.Config

// Game is one Dr Brain game. The host owns the listener, the path prefix and
// the process lifecycle; a game only owns its routes and its state.
type Game interface {
	// Name is the registry key and the name used in logs and /healthz.
	Name() string
	// Prefix is the absolute mount path, e.g. "/mazes".
	Prefix() string
	// API lists the routes the host publishes under Prefix, e.g. "/graphql".
	API() []string
	// Mount registers the game's handlers on mux using absolute paths
	// ("/graphql"). The host mounts mux under Prefix; mux is never exposed at
	// the root.
	Mount(mux *http.ServeMux, deps Deps) error
	// Shutdown persists state and releases resources. It runs once per game,
	// in parallel with the other games, bounded by the host's timeout.
	Shutdown(ctx context.Context) error
}

// Deps is everything the host hands a game during Mount.
type Deps struct {
	// Config is the process-wide platform configuration.
	Config Config
	// Hub is the game's own websocket fan-out instance. Each game's hub is
	// tied to that game's snapshot type, so the host only carries the value
	// through; it constructs one per game when the shared hub lands.
	Hub any
	// StaticDir is the built web app root.
	StaticDir string
	// PublicURL is the external base URL, empty when unknown.
	PublicURL string
	// Logger is scoped to this game.
	Logger *slog.Logger
}

// Optional command interfaces. `drbrain validate`, `mapgen` and `tui` dispatch
// to a game only when it implements the matching interface; games that do not
// need one (or cannot support it) simply leave it out.
type Validator interface {
	Validate(ctx context.Context) error
}

type MapGenerator interface {
	Mapgen(ctx context.Context, args []string) error
}

type TUIRunner interface {
	TUI(ctx context.Context, args []string) error
}

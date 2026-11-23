// Package platform is the contract between the host and a game: the Game
// interface, the per-game dependency bundle and the optional capabilities
// (GraphQL resolvers, llms guides, validators, map generators) the host
// discovers by type assertion.
package platform

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
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
	// Hub is reserved for a shared websocket fan-out and is always nil today.
	// Each game owns its own hub, tied to that game's snapshot type, and the
	// host never populates this field. Read it only with a nil check.
	Hub any
	// StaticDir is the built web app root.
	StaticDir string
	// PublicURL is the external base URL, empty when unknown.
	PublicURL string
	// Logger is scoped to this game.
	Logger *slog.Logger
	// DB is the shared PostgreSQL pool. PostgreSQL is required, so it is
	// always non-nil; a game that needs storage fails Mount when it is nil.
	DB *pgxpool.Pool
	// Accounts is the global account service shared by every game.
	Accounts *account.Service
	// Auth is the global sign-in service (nil when sign-in is disabled).
	Auth *auth.Auth
	// Metrics is the process-wide Prometheus registry served on the host's
	// metrics listener. Games register their collectors here during Mount.
	// Nil when the listener is disabled or a game is mounted standalone.
	Metrics *prometheus.Registry
}

// LLMSProvider lets a game supply its agent guide. The platform renders it at
// /<prefix>/llms.txt; the game never registers that route itself. The template
// receives {BaseURL, WSURL}, where BaseURL already includes the game prefix.
type LLMSProvider interface {
	LLMSTemplate() string
	// LLMSPublicURL is the game-specific base URL, empty to use the platform's.
	LLMSPublicURL() string
}

// Optional command interfaces. `drbrain validate` and `mapgen` dispatch to a
// game only when it implements the matching interface; games that do not need
// one (or cannot support it) simply leave it out.
type Validator interface {
	Validate(ctx context.Context) error
}

// MapGenerator lets `drbrain mapgen` run a game's map generator.
type MapGenerator interface {
	// Mapgen generates a map from the game-specific arguments.
	Mapgen(ctx context.Context, args []string) error
}

// GraphQLResolver lets a game hand its existing gqlgen resolver root to the
// platform's single GraphQL handler. The value stays opaque here so platform
// does not import the game packages; cmd/drbrain type-asserts it when wiring
// the unified executor.
type GraphQLResolver interface {
	GraphQLResolver() any
}

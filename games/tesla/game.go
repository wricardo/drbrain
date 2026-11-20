//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

package tesla

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/wricardo/drbrain/games/tesla/appconfig"
	"github.com/wricardo/drbrain/games/tesla/game/config"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/game/session"
	"github.com/wricardo/drbrain/games/tesla/graph"
	mcptransport "github.com/wricardo/drbrain/games/tesla/transport/mcp"
	"github.com/wricardo/drbrain/games/tesla/transport/websocket"
	"github.com/wricardo/drbrain/platform"
)

const prefix = "/tesla"

// llmsTemplate is the agent guide, rendered by the platform at /tesla/llms.txt.
//
//go:embed llms.txt.tmpl
var llmsTemplate string

// Game is Tesla Road Trip on the platform.
type Game struct {
	publicURL string
	cancel    context.CancelFunc
	resolver  *graph.Resolver
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "tesla" }
func (g *Game) Prefix() string { return prefix }
func (g *Game) API() []string  { return []string{"/mcp"} }

// GraphQLResolver implements platform.GraphQLResolver.
func (g *Game) GraphQLResolver() any { return g.resolver }

// LLMSTemplate implements platform.LLMSProvider.
func (g *Game) LLMSTemplate() string { return llmsTemplate }

// LLMSPublicURL implements platform.LLMSProvider.
func (g *Game) LLMSPublicURL() string { return g.publicURL }

// Mount wires configuration, sessions, GraphQL, playground, MCP and the two
// background maintenance loops. It registers only the API routes; static UI,
// /healthz, the listener and signals are the platform's.
//
// The legacy REST /api/sessions and /ws routes are gone: they existed for the
// TUI and old scripts, both removed in this migration.
func (g *Game) Mount(mux *http.ServeMux, deps platform.Deps) error {
	cfg, err := appconfig.Load()
	if err != nil {
		return fmt.Errorf("TESLA_ environment: %w", err)
	}
	g.publicURL = deps.Config.PublicURL

	configManager, err := config.NewManager(cfg.ConfigDir, cfg.CustomMapsDir)
	if err != nil {
		return fmt.Errorf("create config manager: %w", err)
	}
	persistence, err := session.NewStorePersistence(cfg.SessionsDir, configManager, 0)
	if err != nil {
		return fmt.Errorf("create session persistence: %w", err)
	}
	sessionManager := session.NewManagerWithPersistence(persistence)
	if err := sessionManager.LoadPersistedSessions(); err != nil {
		deps.Logger.Warn("loading persisted sessions", "err", err)
	}
	gameService := service.NewGameService(sessionManager, configManager)

	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	go sessionCleanupRoutine(ctx, sessionManager)
	go filesystemSyncRoutine(ctx, sessionManager, persistence)

	hub := websocket.NewHub(cfg.AllowedOrigins...)
	go hub.Run()

	resolver := graph.NewResolver(gameService, hub, graph.WithAdminPolicy(cfg.Admin()))
	g.resolver = resolver

	if bool(cfg.MCPEnabled) {
		mcpSrv := mcptransport.NewServer(gameService, hub, mcptransport.WithAdminPolicy(cfg.Admin()))
		mux.Handle("/mcp", mcpSrv.Handler())
	}
	deps.Logger.Info("tesla mounted",
		"config_dir", cfg.ConfigDir,
		"sessions_dir", cfg.SessionsDir,
		"introspection", bool(cfg.GraphQLIntrospection),
		"playground", bool(cfg.GraphQLPlayground),
		"mcp", bool(cfg.MCPEnabled))
	return nil
}

// Shutdown stops the maintenance loops. Sessions are already persisted per
// operation, as they were before the migration.
func (g *Game) Shutdown(context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}

// sessionCleanupRoutine periodically removes sessions not accessed within 24h.
func sessionCleanupRoutine(ctx context.Context, manager *session.Manager) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if removed := manager.CleanupExpiredSessions(24 * time.Hour); removed > 0 {
				log.Printf("Cleaned up %d expired sessions", removed)
			}
		}
	}
}

// filesystemSyncRoutine drops in-memory sessions whose files were deleted.
func filesystemSyncRoutine(ctx context.Context, manager *session.Manager, persistence session.SessionPersistence) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if persistence == nil {
				continue
			}
			pruned := 0
			for _, s := range manager.List() {
				if !persistence.Exists(s.ID) {
					if err := manager.DeleteFromMemory(s.ID); err == nil {
						pruned++
					}
				}
			}
			if pruned > 0 {
				log.Printf("Filesystem sync: pruned %d orphaned sessions from memory", pruned)
			}
		}
	}
}

package tesla

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/wricardo/drbrain/games/tesla/appconfig"
	"github.com/wricardo/drbrain/games/tesla/game/config"
	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/game/session"
	"github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/games/tesla/graph/generated"
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
}

// New returns the game for the compiled-in registry.
func New() *Game { return &Game{} }

func (g *Game) Name() string   { return "tesla" }
func (g *Game) Prefix() string { return prefix }
func (g *Game) API() []string  { return []string{"/graphql", "/playground", "/mcp"} }

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
	gqlSrv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	enableGraphQLOperationLogging(gqlSrv)
	if bool(cfg.GraphQLIntrospection) {
		gqlSrv.Use(extension.Introspection{})
	}
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})
	gqlSrv.AddTransport(transport.Options{})
	gqlSrv.AddTransport(&transport.Websocket{
		KeepAlivePingInterval: 10 * time.Second,
		// graphql-transport-ws clients must answer pings; connections that miss
		// pongs for 2x this interval are closed so dead peers do not hold
		// subscriptions open until the kernel TCP timeout.
		PingPongInterval: 10 * time.Second,
		Upgrader:         websocket.NewUpgrader(cfg.AllowedOrigins),
	})
	gqlHandler := withHTTPRequest(gqlSrv)
	mux.Handle("/graphql", gqlHandler)
	mux.Handle("/graphql/", gqlHandler)

	if bool(cfg.GraphQLPlayground) {
		mux.Handle("/playground", playground.Handler("GraphQL playground", prefix+"/graphql"))
	}
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

// withHTTPRequest stores the *http.Request in context so resolvers can read
// headers (e.g. X-Admin-Key).
func withHTTPRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), graph.HTTPRequestKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// enableGraphQLOperationLogging logs every GraphQL operation on completion.
func enableGraphQLOperationLogging(gqlSrv *handler.Server) {
	gqlSrv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		opCtx := graphql.GetOperationContext(ctx)
		opName := "<anonymous>"
		if opCtx.OperationName != "" {
			opName = opCtx.OperationName
		}
		opType := "unknown"
		if opCtx.Operation != nil {
			opType = string(opCtx.Operation.Operation)
		}
		start := time.Now()
		responseHandler := next(ctx)
		return func(ctx context.Context) *graphql.Response {
			resp := responseHandler(ctx)
			errCount := 0
			if resp != nil {
				errCount = len(resp.Errors)
			}
			log.Printf("[graphql] op.end name=%q type=%s duration=%s errors=%d",
				opName, opType, time.Since(start), errCount)
			return resp
		}
	})
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

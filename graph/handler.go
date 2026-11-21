package graph

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"

	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/graph/generated"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/httpserve"
)

// MaxRequestBodyBytes caps GraphQL POST bodies, matching the per-game servers.
const MaxRequestBodyBytes = 1 << 20

const (
	wsPingInterval   = 10 * time.Second
	wsInitTimeout    = 10 * time.Second
	ssePingInterval  = 15 * time.Second
	queryCacheSize   = 1000
	persistedQueries = 100
)

// Options configures the unified GraphQL HTTP surface.
type Options struct {
	// Resolver is the root created with New.
	Resolver *Resolver
	// AllowedOrigins is the comma-separated ALLOWED_ORIGINS list.
	AllowedOrigins string
	// Introspection and Playground default to true; production opts may turn
	// them off.
	Introspection bool
	Playground    bool
	// Auth, when non-nil, resolves the viewer cookie and exposes the sign-in
	// hooks the global account mutations need.
	Auth *auth.Auth
	// MaxBodyBytes defaults to MaxRequestBodyBytes.
	MaxBodyBytes int64
}

// NewHandler builds the single GraphQL handler for every game.
func NewHandler(opts Options) http.Handler {
	if opts.Resolver == nil {
		panic("graph: NewHandler requires a resolver")
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = MaxRequestBodyBytes
	}

	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers:  opts.Resolver,
		Complexity: Complexity(),
	}))

	// SSE first: its Accept header must not be claimed by the plain POST
	// transport. Delivery's watch UI reads it with fetch + ReadableStream.
	srv.AddTransport(transport.SSE{KeepAlivePingInterval: ssePingInterval})
	ws := &transport.Websocket{
		KeepAlivePingInterval: wsPingInterval,
		PingPongInterval:      wsPingInterval,
		InitTimeout:           wsInitTimeout,
		Upgrader:              httpserve.Upgrader(opts.AllowedOrigins),
	}
	if opts.Auth != nil {
		// graphql-ws sends its credentials in connection_init, not as HTTP
		// headers, so the upgrade request's viewer is not enough.
		ws.InitFunc = func(ctx context.Context, payload transport.InitPayload) (context.Context, *transport.InitPayload, error) {
			raw := auth.BearerToken(payload.Authorization())
			if raw == "" {
				return ctx, nil, nil
			}
			// A bad or stale token is treated as anonymous: public subscriptions
			// (a watch link) must still work.
			if acct, err := opts.Auth.ParseToken(raw); err == nil {
				return auth.WithViewer(ctx, &auth.Viewer{Account: acct, Token: raw}), nil, nil
			}
			return ctx, nil, nil
		}
	}
	srv.AddTransport(ws)
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.MultipartForm{})

	srv.SetQueryCache(lru.New[*ast.QueryDocument](queryCacheSize))
	if opts.Introspection {
		srv.Use(extension.Introspection{})
	}
	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](persistedQueries)})
	srv.Use(extension.FixedComplexityLimit(ComplexityLimit))
	srv.AroundOperations(logOperation)

	base := withRequestContext(opts.Auth, srv)
	base = limitBody(maxBody, base)
	return httpserve.WithCORS(opts.AllowedOrigins, base)
}

// Playground serves the query editor at /graphql/playground.
func Playground() http.Handler {
	return playground.Handler("Dr Brain", "/graphql")
}

// withRequestContext shares the *http.Request and the global viewer with every
// namespace. The per-game request keys stay because game resolvers read them.
func withRequestContext(a *auth.Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithRequest(r.Context(), r)
		ctx = context.WithValue(ctx, mazesgraph.HTTPRequestKey{}, r)
		ctx = context.WithValue(ctx, motorgraph.HTTPRequestKey{}, r)
		ctx = context.WithValue(ctx, teslagraph.HTTPRequestKey{}, r)

		if a != nil {
			if acct, token := a.Viewer(r); acct != nil {
				ctx = auth.WithViewer(ctx, &auth.Viewer{Account: acct, Token: token})
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// limitBody caps request bodies before gqlgen decodes them.
func limitBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// logOperation logs every GraphQL operation on completion.
func logOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	opCtx := graphql.GetOperationContext(ctx)
	name := opCtx.OperationName
	if name == "" {
		name = "<anonymous>"
	}
	start := time.Now()
	rh := next(ctx)
	return func(ctx context.Context) *graphql.Response {
		resp := rh(ctx)
		if resp != nil {
			log.Printf("[graphql] op=%q duration=%s errors=%d", name, time.Since(start), len(resp.Errors))
		}
		return resp
	}
}

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
	motorauth "github.com/wricardo/drbrain/games/motor/auth"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	"github.com/wricardo/drbrain/graph/generated"
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
	// Auth, when non-nil, resolves the motor viewer cookie and exposes the
	// sign-in hooks the password mutations need.
	Auth *motorauth.Auth
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
	srv.AddTransport(&transport.Websocket{
		KeepAlivePingInterval: wsPingInterval,
		PingPongInterval:      wsPingInterval,
		InitTimeout:           wsInitTimeout,
		Upgrader:              httpserve.Upgrader(opts.AllowedOrigins),
	})
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

// withRequestContext shares the *http.Request with every game's resolvers. The
// keys live in each game package, so all three are set; delivery reads none.
// Motor's viewer/sign-in middleware is ported here because the unified handler
// is the only transport now.
func withRequestContext(a *motorauth.Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, mazesgraph.HTTPRequestKey{}, r)
		ctx = context.WithValue(ctx, motorgraph.HTTPRequestKey{}, r)
		ctx = context.WithValue(ctx, teslagraph.HTTPRequestKey{}, r)

		if a != nil && (r.Method == http.MethodPost || isWebSocketUpgrade(r)) {
			upgrade := isWebSocketUpgrade(r)
			if acct, token := a.Viewer(r); acct != nil {
				v := &motorgraph.Viewer{
					Account:      acct,
					Token:        token,
					ClearSession: func() { a.ClearSession(w, r) },
				}
				ctx = context.WithValue(ctx, motorgraph.ViewerKey{}, v)
			}
			// A websocket upgrade has no HTTP response to set a cookie on, so a
			// password mutation there is refused instead of minting a login the
			// browser could never send back.
			signIn := &motorgraph.SignIn{
				OverHTTP:   !upgrade,
				SetSession: func(token string, expires time.Time) { a.SetSession(w, r, token, expires) },
			}
			ctx = context.WithValue(ctx, motorgraph.SignInKey{}, signIn)
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

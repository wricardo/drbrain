package graph

import (
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/generated"
)

// Handler returns the GraphQL endpoint. GraphQL is the only transport: there is
// no REST surface to keep in sync, so the schema is the contract.
//
// Complexity limiting is deliberately NOT enabled. Every field on Snapshot is a
// pre-built value from one snapshot read, so the cost is one session lock
// acquisition regardless of how deep a client queries; a depth limit would only
// block legitimate "give me everything" calls.
//
// Responses are gzipped: a snapshot is highly repetitive JSON (1.3 MB raw,
// 133 KB gzipped on a 42k-tick save), so the encoding is what keeps a live
// stream affordable. Streams are excluded — see gzipHandler — because buffering
// is what makes the encoding possible and a stream never ends.
func Handler(mgr *session.Manager) http.Handler {
	return gzipHandler(newServer(mgr))
}

// UncompressedHandler is Handler without its buffering gzip layer. The platform
// host gzips game responses at the mount and streams (SSE, websocket) bypass
// that layer, so a game mounted there must not compress again.
func UncompressedHandler(mgr *session.Manager) http.Handler {
	return newServer(mgr)
}

func newServer(mgr *session.Manager) http.Handler {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{Mgr: mgr},
	}))

	// SSE carries the subscription: the UI reads it with fetch + ReadableStream,
	// so it needs no GraphQL client library. Websocket is kept as well because
	// agents and the playground already use it, and it costs one line.
	srv.AddTransport(transport.SSE{
		// Comments every 15s keep intermediaries from closing an idle stream.
		// A paused session can legitimately go minutes without a change.
		KeepAlivePingInterval: 15 * time.Second,
	})
	srv.AddTransport(transport.Websocket{KeepAlivePingInterval: 10 * time.Second})
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.MultipartForm{})

	// These three come from NewDefaultServer, which this handler deliberately
	// does not use: that constructor hardcodes exactly the transport list, and
	// SSE has to be added first so the UI's `Accept: text/event-stream` request
	// is not claimed by the plain POST transport. Everything the constructor
	// did that is still wanted has to be re-added by hand, or the drop is
	// silent. Introspection was the one that bit: without it
	// `{ __schema { queryType { name } } }` returns "introspection disabled",
	// which hides the schema from the playground, from agents exploring the
	// API, and from cmd/gentypes' own assumptions about the schema.
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	return srv
}

// Playground serves an interactive query editor at /graphql/playground.
func Playground() http.Handler { return PlaygroundAt("/graphql") }

// PlaygroundAt targets endpoint, e.g. /delivery/graphql behind the platform.
func PlaygroundAt(endpoint string) http.Handler {
	return playground.Handler("DrBrain Delivery", endpoint)
}

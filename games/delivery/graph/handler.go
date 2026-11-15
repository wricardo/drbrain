package graph

import (
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"

	"github.com/wricardo/drbrain-delivery/game/session"
	"github.com/wricardo/drbrain-delivery/graph/generated"
)

// Handler returns the GraphQL endpoint. GraphQL is the only transport: there is
// no REST surface to keep in sync, so the schema is the contract.
//
// Complexity limiting is deliberately NOT enabled. Every field on Snapshot is a
// pre-built value from one snapshot read, so the cost is one session lock
// acquisition regardless of how deep a client queries; a depth limit would only
// block legitimate "give me everything" calls.
func Handler(mgr *session.Manager) http.Handler {
	srv := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{Mgr: mgr},
	}))
	return srv
}

// Playground serves an interactive query editor at /graphql/playground.
func Playground() http.Handler {
	return playground.Handler("DrBrain Delivery", "/graphql")
}

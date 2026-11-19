// Package graph is the GraphQL transport: schema, resolvers and the HTTP
// handler that serves it.
//
// GraphQL is the only transport. The engine and session layers know nothing
// about it, so every rule stays testable without a schema in the way.
package graph

import "github.com/wricardo/drbrain-delivery/game/session"

// Resolver is the root resolver. It holds no state of its own: everything is
// reached through the session manager, so a resolver call is always
// short-lived and safe to make concurrently.
type Resolver struct {
	Mgr *session.Manager
}

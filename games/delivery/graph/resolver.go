// Package graph is the GraphQL transport: schema, resolvers and the HTTP
// handler that serves it.
//
// GraphQL is the only transport. The engine and session layers know nothing
// about it, so every rule stays testable without a schema in the way.
package graph

import (
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/platform/account"
)

// Resolver is the root resolver. It holds no state of its own beyond the graph
// dependencies: everything is reached through the session manager, so a
// resolver call is always short-lived and safe to make concurrently.
type Resolver struct {
	Mgr *session.Manager
	// Accounts is the global account service, used only to render a session's
	// owner as a public name and handle. Nil keeps the game guest-only (the
	// zero value in tests): ownership checks still work from stored ids/hashes.
	Accounts *account.Service
}

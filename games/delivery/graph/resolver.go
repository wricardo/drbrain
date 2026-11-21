// Package graph is Delivery's GraphQL transport: the resolver tree, engine/session
// conversions, authorization rules, and the split SDL. The HTTP executor lives
// in the host's unified graph package; Delivery has no per-game endpoint.
//
// GraphQL is the only transport. The engine and session layers know nothing
// about it, so every rule stays testable without a schema in the way.
package graph

import (
	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/platform/account"
)

// Resolver is the root resolver. It holds no state of its own beyond the graph
// dependencies: everything is reached through the session manager and city
// catalog, so a resolver call is always short-lived and safe to make
// concurrently.
type Resolver struct {
	Mgr *session.Manager
	// Catalog is the city catalogue: builtin files plus community rows.
	Catalog *cityconfig.Manager
	// Accounts is the global account service, used only to render a session's
	// owner as a public name and handle. Nil keeps the game guest-only (the
	// zero value in tests): ownership checks still work from stored ids/hashes.
	Accounts *account.Service
	// AdminKey gates shipped and legacy ownerless city writes. Requests must
	// send it as X-Admin-Key. Empty disables those writes unless
	// AllowUnauthenticatedAdmin opts out for local development.
	AdminKey string
	// AllowUnauthenticatedAdmin is honoured only when AdminKey is empty.
	AllowUnauthenticatedAdmin bool
}

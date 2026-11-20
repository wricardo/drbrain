// Package graph is the single GraphQL surface for every game.
//
// One generated executor serves namespaced queries and mutations and flat
// prefixed subscriptions. The per-game graph packages still own their domain
// logic and models; this package only binds them into one schema.
//
//go:generate go run ../cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate
//go:generate go run ../cmd/gentypes
package graph

import (
	"errors"

	deliverygraph "github.com/wricardo/drbrain/games/delivery/graph"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
)

// errGameDisabled is returned when a query touches a namespace whose game was
// not enabled with DRBRAIN_GAMES.
var errGameDisabled = errors.New("game is not enabled")

// Resolver is the gqlgen root. Each namespace field returns the game resolver
// object it was bound to, so field adapters are delegations, not copies.
type Resolver struct {
	Delivery *deliverygraph.Resolver
	Mazes    *mazesgraph.Resolver
	Motor    *motorgraph.Resolver
	Tesla    *teslagraph.Resolver
}

// New returns the root resolver for the enabled games. A nil game resolver is
// a programming error: the host only serves the games it mounted.
func New(delivery *deliverygraph.Resolver, mazes *mazesgraph.Resolver, motor *motorgraph.Resolver, tesla *teslagraph.Resolver) *Resolver {
	return &Resolver{Delivery: delivery, Mazes: mazes, Motor: motor, Tesla: tesla}
}

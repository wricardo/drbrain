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
	deliverygenerated "github.com/wricardo/drbrain/games/delivery/graph/generated"
	mazesgraph "github.com/wricardo/drbrain/games/mazes/graph"
	motorgraph "github.com/wricardo/drbrain/games/motor/graph"
	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	teslagraph "github.com/wricardo/drbrain/games/tesla/graph"
	accountgraph "github.com/wricardo/drbrain/platform/account/graph"
)

// errGameDisabled is returned when a query touches a namespace whose game was
// not enabled with DRBRAIN_GAMES.
var errGameDisabled = errors.New("game is not enabled")

// Resolver is the gqlgen root. Each namespace field returns the game resolver
// object it was bound to, so field adapters are delegations, not copies.
type Resolver struct {
	Account  *accountgraph.Resolver
	Delivery *deliverygraph.Resolver
	Mazes    *mazesgraph.Resolver
	Motor    *motorgraph.Resolver
	Tesla    *teslagraph.Resolver
	Music    *musicgraph.Resolver
}

// New returns the root resolver for the enabled games. A nil game resolver
// disables that namespace: its root field returns errGameDisabled, which is how
// cmd/drbrain hides games not selected with DRBRAIN_GAMES.
func New(account *accountgraph.Resolver, delivery *deliverygraph.Resolver, mazes *mazesgraph.Resolver, motor *motorgraph.Resolver, tesla *teslagraph.Resolver, music *musicgraph.Resolver) *Resolver {
	return &Resolver{Account: account, Delivery: delivery, Mazes: mazes, Motor: motor, Tesla: tesla, Music: music}
}

// deliveryMutation returns the Delivery game's mutation resolvers. Delivery
// mutations are flat root fields (deliveryMoveCar, ...) rather than a
// namespace object, so each adapter resolves the game here and returns
// errGameDisabled when DRBRAIN_GAMES left delivery out.
func (r *Resolver) deliveryMutation() (deliverygenerated.MutationResolver, error) {
	if r.Delivery == nil {
		return nil, errGameDisabled
	}
	return r.Delivery.Mutation(), nil
}

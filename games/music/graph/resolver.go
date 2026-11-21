// Package graph is the Music Region GraphQL surface: gqlgen resolvers, the
// flat subscription forwarder and conversions from service types to schema
// models. Every resolver delegates to the game service; no game rule lives
// here.
package graph

import (
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/transport/websocket"
)

// Resolver is the gqlgen dependency injection root.
type Resolver struct {
	Service service.GameService
	Hub     *websocket.Hub
}

// NewResolver builds the resolver root from the game service and the
// subscription hub.
func NewResolver(svc service.GameService, hub *websocket.Hub) *Resolver {
	return &Resolver{Service: svc, Hub: hub}
}

// subscriptionBuffer is the output channel size of the subscription forwarder.
const subscriptionBuffer = 8

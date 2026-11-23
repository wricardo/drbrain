package model

import "time"

// Snapshot is one session's full client view at a tick.
//
// Hand-written rather than generated because orders and openOrders carry
// filter arguments: gqlgen binds those to resolvers instead of struct fields
// (see fields: in games/delivery/gqlgen.yml), and a bound type is never emitted
// into models_gen.go. Everything else stays a plain struct field.
//
// Keep the exported fields in sync with schema.graphqls: gqlgen binds them by
// name, so a schema field without a matching field here fails generation.
type Snapshot struct {
	ID        string
	CreatedAt time.Time
	// DisplayName is the player's name for this save; empty falls back to the
	// city name.
	DisplayName string

	Tick int
	// Clock multiplier: 0 paused, 1/2/4 running.
	Speed  int
	Paused bool
	// Wall-clock length of one tick at 1x, from the city config.
	TickMs int

	City *CityView
	Cars []*CarView
	// Orders is the full history, newest created first. The orders resolver
	// filters it; see schema.graphqls for the arguments.
	Orders []*OrderView
	// OpenOrders is the unfinished subset, prebuilt so the common read costs
	// no per-field scan.
	OpenOrders []*OrderView
	Ledger     *LedgerView

	// CarClasses is the full catalogue with per-session unlock/affordability
	// flags, cheapest first.
	CarClasses []*CarClassView
	// Growth is the city's scheduled expansion, in tick order.
	Growth []*GrowthView

	// Events is the retained feed, oldest first.
	Events []*EventView
	// EventCapacity is how many events the feed can retain.
	EventCapacity int

	// Closures are the street works live right now.
	Closures []*ClosureView
}

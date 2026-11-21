// Package engine holds the DrBrain Delivery world model: terrain, cars,
// orders, the tick loop that advances them, and the rules that govern
// movement and deliveries.
//
// The engine is deliberately transport-agnostic. GraphQL, the web UI and MCP
// are all adapters over the values defined here; nothing in this package
// knows about HTTP, JSON responses or rendering.
package engine

// CellType is the terrain at a grid coordinate.
//
// Terrain is the only thing stored in the layout grid. Anything with business
// data (menus, demands, capacity, timers) lives in an entity attached to a
// coordinate — see Kitchen, Customer, Depot, Charger.
type CellType string

const (
	WaterTile    CellType = "water"    // impassable
	BuildingTile CellType = "building" // impassable scenery
	RoadTile     CellType = "road"     // passable, normal step cost
	GroundTile   CellType = "ground"   // passable, higher step cost
	DepotTile    CellType = "depot"    // HQ: parks and recharges cars
	ChargerTile  CellType = "charger"  // restores battery
	KitchenTile  CellType = "kitchen"  // pickup point
	HouseTile    CellType = "house"    // dropoff point
)

// Grid bounds. A city must be at least large enough to hold a meaningful
// route; the upper bound keeps the ASCII renderer and pathfinding sane.
const (
	MinGridSize = 5
	MaxGridSize = 60
)

// Passable reports whether a car may ever enter this terrain.
func (t CellType) Passable() bool {
	switch t {
	case WaterTile, BuildingTile:
		return false
	}
	return true
}

// Charges reports whether standing here can recharge a car in the tick loop:
// only depot and standalone charger tiles do. Road, ground, kitchen and house
// tiles never charge, even when an entity sits on them.
func (t CellType) Charges() bool {
	return t == DepotTile || t == ChargerTile
}

// Position is a grid coordinate. X is the column (east+), Y is the row
// (south+), matching the row-major layout strings in city.json.
type Position struct {
	// X is the column, east positive.
	X int `json:"x"`

	// Y is the row, south positive.
	Y int `json:"y"`
}

// Neighbour returns the position one step in the given direction.
// The boolean is false for an unknown direction.
func (p Position) Neighbour(dir Direction) (Position, bool) {
	switch dir {
	case North:
		return Position{p.X, p.Y - 1}, true
	case South:
		return Position{p.X, p.Y + 1}, true
	case West:
		return Position{p.X - 1, p.Y}, true
	case East:
		return Position{p.X + 1, p.Y}, true
	}
	return p, false
}

// String renders a position as "(x,y)" for logs and messages.
func (p Position) String() string {
	return "(" + itoa(p.X) + "," + itoa(p.Y) + ")"
}

// Direction is a single grid step. Manual driving is expressed entirely in
// terms of these; there is no autopilot.
type Direction string

const (
	North Direction = "north"
	South Direction = "south"
	East  Direction = "east"
	West  Direction = "west"
)

// ParseDirection accepts the north/south/east/west vocabulary plus the
// up/down/left/right aliases used by keyboard input and many API clients.
func ParseDirection(s string) (Direction, bool) {
	switch s {
	case "north", "up":
		return North, true
	case "south", "down":
		return South, true
	case "east", "right":
		return East, true
	case "west", "left":
		return West, true
	}
	return "", false
}

// Valid reports whether d is one of the four cardinal directions.
func (d Direction) Valid() bool {
	switch d {
	case North, South, East, West:
		return true
	}
	return false
}

// AllDirections is the fixed evaluation order used by the tick loop, so a
// replay of the same commands always produces the same world.
var AllDirections = []Direction{North, South, East, West}

// itoa avoids importing strconv in this file for one call site.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

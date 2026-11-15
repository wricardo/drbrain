package engine

import "fmt"

// CarState is the coarse status of a car, shown in the UI and used by rules.
//
// A car is IDLE when parked and empty, CARRYING when it holds at least one
// order, CHARGING when it is standing on a charging tile, and STRANDED when it
// has run out of battery away from a charger.
type CarState string

const (
	CarIdle     CarState = "idle"
	CarCarrying CarState = "carrying"
	CarCharging CarState = "charging"
	CarStranded CarState = "stranded"
)

// RejectReason explains why a queued move did not happen. Every rejection is
// surfaced to the player; a move never fails silently.
type RejectReason string

const (
	RejectNone         RejectReason = ""
	RejectBadDirection RejectReason = "bad_direction"
	RejectOffGrid      RejectReason = "off_grid"
	RejectBlocked      RejectReason = "blocked" // water, building, unexplored
	RejectNoBattery    RejectReason = "no_battery"
)

// Car is one deliverable vehicle, driven manually by the player.
//
// There is no pathfinding for the player: a car moves only when a command
// queues a direction for it, and it executes at most one step per tick. That
// is what makes multi-car play with a tick clock unambiguous — each car has
// its own input slot instead of competing for a shared turn.
type Car struct {
	ID   string
	Name string

	// ClassName is the catalogue key this car was built from ("scooter",
	// "van", …). It is stored explicitly rather than inferred from stats: two
	// classes may share the same capacity, and a UI must show the real name.
	ClassName string
	Class     CarClassConfig

	Pos     Position
	Battery int
	Cargo   []string // order ids currently held

	// cargoBoxes is how many boxes Cargo amounts to. It is derived from Cargo
	// plus the order sizes and is rebuilt after load, so it can never drift
	// from the truth. Storing it avoids walking the order list on every
	// capacity check.
	cargoBoxes int `json:"-"`

	// QueuedMove is the direction this car will attempt on the next tick.
	// Empty means "stand still". A new command overwrites it, so a fast client
	// cannot accumulate a backlog of steps.
	QueuedMove Direction

	// LastReject records why the most recent attempted move failed, for the
	// player's benefit. It is cleared on a successful move.
	LastReject RejectReason

	// StepsTaken counts successful moves, used by the sim harness.
	StepsTaken int
}

// State reports the car's coarse status.
//
// Order matters: empty battery parked on a charger is "charging", not
// "stranded", because it is recovering rather than stuck.
func (c *Car) State(city *City) CarState {
	onCharger := city.ChargingAt(c.Pos)
	if c.Battery <= 0 {
		if onCharger {
			return CarCharging
		}
		return CarStranded
	}
	if len(c.Cargo) > 0 {
		return CarCarrying
	}
	if onCharger && !c.Full() {
		return CarCharging
	}
	return CarIdle
}

// Full reports whether the battery is at capacity.
func (c *Car) Full() bool { return c.Battery >= c.Class.MaxBattery }

// Load is how many boxes the car is carrying right now.
//
// Capacity is measured in BOXES, not orders: a customer can ask for forty
// pizzas, and whether that fits is a property of the vehicle rather than of
// how many order ids happen to be in the hold. Load is maintained on pickup
// and dropoff and recomputed from cargo on load, so it is derived state that
// survives a save without being a second source of truth.
func (c *Car) Load() int { return c.cargoBoxes }

// FreeBoxes is the remaining hold space.
func (c *Car) FreeBoxes() int {
	free := c.Class.Capacity - c.cargoBoxes
	if free < 0 {
		return 0
	}
	return free
}

// HasSpace reports whether the car can take at least one more box.
func (c *Car) HasSpace() bool { return c.FreeBoxes() > 0 }

// SpaceFor reports whether an order of the given size fits in the hold.
func (c *Car) SpaceFor(quantity int) bool {
	if quantity <= 0 {
		quantity = 1
	}
	return quantity <= c.FreeBoxes()
}

// addCargo records that the car now holds boxes for an order.
func (c *Car) addCargo(orderID string, boxes int) {
	if boxes <= 0 {
		boxes = 1
	}
	c.Cargo = append(c.Cargo, orderID)
	c.cargoBoxes += boxes
}

// removeCargo drops an order from the hold and frees its boxes.
func (c *Car) removeCargo(orderID string, boxes int) {
	if boxes <= 0 {
		boxes = 1
	}
	for i, id := range c.Cargo {
		if id == orderID {
			c.Cargo = append(c.Cargo[:i], c.Cargo[i+1:]...)
			break
		}
	}
	c.cargoBoxes -= boxes
	if c.cargoBoxes < 0 {
		c.cargoBoxes = 0
	}
}

// BatteryPct returns charge as a 0-100 percentage.
func (c *Car) BatteryPct() int {
	if c.Class.MaxBattery <= 0 {
		return 0
	}
	return c.Battery * 100 / c.Class.MaxBattery
}

// String renders a car for logs and the CLI status line.
func (c *Car) String() string {
	return fmt.Sprintf("%s %s at %s battery %d/%d cargo %d/%d boxes",
		c.ID, c.Name, c.Pos, c.Battery, c.Class.MaxBattery, c.cargoBoxes, c.Class.Capacity)
}

// NewCar builds a car of the named class at pos with a full battery.
func NewCar(id, name, class string, pos Position, classes map[string]CarClassConfig) (*Car, error) {
	cc, ok := classes[class]
	if !ok {
		return nil, fmt.Errorf("unknown car class %q", class)
	}
	if cc.MaxBattery <= 0 {
		return nil, fmt.Errorf("car class %q has non-positive max_battery", class)
	}
	if cc.Capacity <= 0 {
		return nil, fmt.Errorf("car class %q has non-positive capacity", class)
	}
	if cc.StepCost <= 0 {
		cc.StepCost = 1
	}
	return &Car{
		ID:        id,
		Name:      name,
		ClassName: class,
		Class:     cc,
		Pos:       pos,
		Battery:   cc.MaxBattery,
	}, nil
}

// Clone returns a deep copy so a snapshot handed to a spectator cannot be
// mutated by the next tick.
func (c *Car) Clone() *Car {
	out := *c
	out.Cargo = append([]string(nil), c.Cargo...)
	return &out
}

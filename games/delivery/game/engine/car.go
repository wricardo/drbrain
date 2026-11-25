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
//
// The checks run in a fixed order and the FIRST failure wins, so a move that is
// both off-battery and heading into water reports the battery:
//
//  0. no queued move at all  -> not a rejection, nothing is recorded
//  1. battery <= 0           -> no_battery
//  2. invalid direction      -> bad_direction
//  3. destination closed     -> closed
//  4. off-grid               -> off_grid
//  5. water / building / fog -> blocked
//  6. tile cost > battery    -> no_battery
//  7. destination full       -> occupied
//
// Step 3 before 4 matters for the map edge: a closure on the border is still
// reported as `closed`. Step 4 is distinct from 5 on purpose — an earlier
// version reported the grid edge as "unexplored", which made a map boundary
// look like a district the player had not bought yet.
//
// Occupancy is last because it is the only check whose answer depends on what
// the OTHER cars did this tick. Putting it earlier would report "occupied" for a
// move that also had no battery, which reads as a traffic problem when the car
// is simply flat — and the player's fix (charge) would be the wrong one.
//
// A successful step resets the field to RejectNone, so a non-empty value always
// describes the most recent failure rather than the most recent command.
type RejectReason string

const (
	RejectNone         RejectReason = ""
	RejectBadDirection RejectReason = "bad_direction"
	RejectOffGrid      RejectReason = "off_grid"
	RejectBlocked      RejectReason = "blocked" // water, building, unexplored
	RejectNoBattery    RejectReason = "no_battery"
	RejectClosed       RejectReason = "closed"   // street works
	RejectOccupied     RejectReason = "occupied" // another car holds the tile
)

// Car is one deliverable vehicle, driven manually by the player.
//
// There is no pathfinding for the player: a car moves only when a command
// queues a direction for it, and it executes at most one step per tick. That
// is what makes multi-car play with a tick clock unambiguous — each car has
// its own input slot instead of competing for a shared turn.
type Car struct {
	// ID is the stable identifier used by commands, saves and events
	// ("car_1", "car_2", …).
	ID string
	// Name is display text for the fleet list and event feed. Authored cars
	// default to "Car N"; the name never keys a rule.
	Name string

	// ClassName is the catalogue key this car was built from ("scooter",
	// "van", …). It is stored explicitly rather than inferred from stats: two
	// classes may share the same capacity, and a UI must show the real name.
	ClassName string
	// Class is the copied catalogue entry (capacity, battery, step cost,
	// price) so a car keeps the stats it was built with.
	Class CarClassConfig
	// PurchasePrice is what the player actually paid for this car, kept so a
	// resale can return a share of that amount rather than the current list
	// price. It is -1 for authored, granted and starter cars, which cost
	// nothing and have no resale value. Saves written before this field
	// existed decode as 0; ImportState backfills those from the car's purchase
	// entry in the cash ledger, so a legacy bought car keeps its true price.
	PurchasePrice int `json:"purchase_price,omitempty"`

	// Pos is the car's current tile. Movement is one grid step per tick.
	Pos Position
	// Battery is current charge in battery units. It drains by the tile's walk
	// cost and is capped at Class.MaxBattery; at or below zero the car cannot
	// move (RejectNoBattery) and must charge or be towed.
	Battery int
	// Cargo holds one order id per order on board, not one per box. Order sizes
	// live on the orders; cargoBoxes is their summed size.
	Cargo []string

	// ChargeDue and ChargeUnits are the open paid charging session: what the
	// car has run up at a standalone charger, and how much battery it bought.
	// They are persisted so a save taken mid-charge resumes with the same bill,
	// and settled (written to the cash ledger) when the car fills or leaves.
	// ChargeSource is the charger id (or "charger" when unnamed) used for the
	// ledger memo; moving to another paid source settles the old bill first.
	ChargeDue    int
	ChargeUnits  int
	ChargeSource string

	// cargoBoxes is how many boxes Cargo amounts to. It is derived from Cargo
	// plus the order sizes and is rebuilt after load, so it can never drift
	// from the truth. Storing it avoids walking the order list on every
	// capacity check.
	cargoBoxes int `json:"-"`

	// QueuedMove is the direction this car will attempt on the next tick: the
	// head of its movement queue. Empty means "stand still".
	QueuedMove Direction
	// MoveQueue holds the steps after QueuedMove, in the order they will be
	// attempted. A car executes at most one step per tick, so a long queue is a
	// route that unfolds over several ticks. Existing saves have no such field;
	// its absence is simply an empty queue.
	MoveQueue []Direction `json:",omitempty"`

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

// Full reports whether the battery has reached Class.MaxBattery. It is
// battery-only: a full car may still have free hold space, and a full hold does
// not stop charging. Full cars skip a charging tick and do not occupy a charge
// slot.
func (c *Car) Full() bool { return c.Battery >= c.Class.MaxBattery }

// CarResalePercent is the share of the purchase price a player gets back when
// selling a vehicle. The loss is the point: buying and flipping must not be a
// money loop.
const CarResalePercent = 70

// PaidPrice is what the player paid, with free authored/starter cars reported
// as 0 rather than the internal "not bought" sentinel.
func (c *Car) PaidPrice() int {
	if c.PurchasePrice < 0 {
		return 0
	}
	return c.PurchasePrice
}

// SaleValue is the cash returned by selling this car: CarResalePercent of what
// the player paid, rounded down to whole dollars. Starter, granted and legacy
// cars with no recorded price are worth nothing: the class list price is not
// evidence of a payment.
func (c *Car) SaleValue() int {
	if c.PurchasePrice <= 0 {
		return 0
	}
	return c.PurchasePrice * CarResalePercent / 100
}

// Load is how many boxes the car is carrying right now.
//
// Capacity is measured in BOXES, not orders: a customer can ask for forty
// pizzas, and whether that fits is a property of the vehicle rather than of
// how many order ids happen to be in the hold. Load is maintained on pickup
// and dropoff and recomputed from cargo on load, so it is derived state that
// survives a save without being a second source of truth.
func (c *Car) Load() int { return c.cargoBoxes }

// FreeBoxes is how many more boxes the hold can take: Capacity minus the boxes
// already carried, floored at 0. Capacity is in boxes, not order slots, and
// pickup admits an order only when SpaceFor(quantity).
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
//
// A quantity of zero or less is treated as one box rather than as a no-op, so a
// malformed order from a map file cannot be accepted as "fits, takes no room".
// Verified on a 4-box scooter: -5 and 0 both report true, and 5 reports false.
// The coercion makes SpaceFor permissive about nonsense quantities, so this is a
// capacity check, not a validation step — validate the quantity where the order
// is constructed.
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

// BatteryPct is charge as an integer 0-100 percentage of Class.MaxBattery. The
// division truncates, and a non-positive MaxBattery reports 0.
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

// NewCar builds a car of the named class at pos with a full battery. It rejects
// an unknown class and a class with non-positive max_battery or capacity, so a
// constructed car always has a usable hold and a finite charge.
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
		ID:            id,
		Name:          name,
		ClassName:     class,
		Class:         cc,
		Pos:           pos,
		Battery:       cc.MaxBattery,
		PurchasePrice: -1, // authored/starter car until BuyCar records a price
	}, nil
}

// Clone returns a deep copy so a save or a snapshot cannot be mutated by the
// next tick. Cargo is copied; Class is a value and comes along with the struct.
func (c *Car) Clone() *Car {
	out := *c
	out.Cargo = append([]string(nil), c.Cargo...)
	out.MoveQueue = append([]Direction(nil), c.MoveQueue...)
	return &out
}

// PendingMoves returns the queued directions in execution order: the step for
// the next tick first, then the rest of the backlog. It is nil when the car has
// no pending step.
func (c *Car) PendingMoves() []Direction {
	if c.QueuedMove == "" && len(c.MoveQueue) == 0 {
		return nil
	}
	out := make([]Direction, 0, 1+len(c.MoveQueue))
	if c.QueuedMove != "" {
		out = append(out, c.QueuedMove)
	}
	return append(out, c.MoveQueue...)
}

// pendingMoveCount is PendingMoves without the allocation, for the queue cap.
func (c *Car) pendingMoveCount() int {
	n := len(c.MoveQueue)
	if c.QueuedMove != "" {
		n++
	}
	return n
}

// popMove removes the step for the next tick and promotes the backlog. It
// returns "" when nothing is queued. A rejected step is still spent: the queue
// is a plan, and a blocked plan must not stall the rest of it forever.
func (c *Car) popMove() Direction {
	dir := c.QueuedMove
	if len(c.MoveQueue) > 0 {
		c.QueuedMove = c.MoveQueue[0]
		c.MoveQueue = c.MoveQueue[1:]
	} else {
		c.QueuedMove = ""
	}
	return dir
}

// clearMoves drops the whole movement plan. Towing and a stale route both need
// this: a preloaded backlog must never steer a car after the player has
// abandoned the plan.
func (c *Car) clearMoves() {
	c.QueuedMove = ""
	c.MoveQueue = nil
}

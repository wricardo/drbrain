package engine

import (
	"fmt"
	"sort"
)

// OrderState is where an order is in its lifecycle.
//
// PENDING     raised by a customer, nobody has taken it
// ASSIGNED    a car is expected to collect it
// PICKED_UP   the assigned car holds it and is heading for the customer
// DONE        delivered
// EXPIRED     the deadline passed before delivery
type OrderState string

const (
	OrderPending  OrderState = "pending"
	OrderAssigned OrderState = "assigned"
	OrderPickedUp OrderState = "picked_up"
	OrderDone     OrderState = "done"
	OrderExpired  OrderState = "expired"
)

// Terminal reports whether the order can no longer change state: DONE or
// EXPIRED. PENDING, ASSIGNED and PICKED_UP are live; the spawn cap, OpenOrders
// and ActiveOrderCount all use this split.
func (s OrderState) Terminal() bool {
	return s == OrderDone || s == OrderExpired
}

// Order is one delivery job.
//
// The assignment is advisory in the sense that the player chooses which car,
// but once assigned only that car may collect it. That keeps "who is carrying
// what" unambiguous with several cars on the road.
type Order struct {
	// ID is stable for the session and is what commands, cargo and ledger rows
	// reference.
	ID string
	// Item is an item id from CityConfig.Items; the display label is separate.
	Item string
	// KitchenID is the authored pickup site; CustomerID is the authored
	// dropoff site.
	KitchenID  string
	CustomerID string

	// Quantity is how many boxes the customer asked for, and it is what makes
	// vehicle class a routing decision rather than a cosmetic upgrade: a
	// forty-box catering order simply does not fit in a scooter. Demand may
	// exceed the current fleet on purpose; the order stays pending until a
	// capable car exists, which is the pressure to upgrade.
	Quantity int

	// CreatedTick is when the customer raised the order. DeadlineTick is the
	// on-time cutoff; assigning a car can extend it but never shortens it.
	CreatedTick  int64
	DeadlineTick int64

	// Reward is the full on-time payment; a late delivery pays
	// OrdersConfig.LatePayMultiplier of it. Penalty is the cash fine charged on
	// expiry (recorded as CashOrderFine), not the reputation loss.
	Reward  int
	Penalty int

	// AssignedCar is the one car allowed to collect the order ("" while
	// pending). State is the lifecycle position.
	AssignedCar string
	State       OrderState

	// DeliveredTick is when the dropoff happened; Paid is what was actually
	// paid (late deliveries pay less than Reward). Late is set when the
	// delivery landed after DeadlineTick.
	DeliveredTick int64
	Paid          int
	Late          bool
}

// Boxes is the hold space this order occupies.
//
// It normalises a missing quantity to one box. That is not defensive
// padding: saves written before quantities existed store orders with no size,
// and a zero would let a car carry an unbounded number of them.
func (o *Order) Boxes() int {
	if o.Quantity <= 0 {
		return 1
	}
	return o.Quantity
}

// TicksLeft is the remaining time before the deadline.
//
// It is signed: an overdue order that has not been swept yet reports a
// negative number rather than clamping to zero, so a client can tell "due
// this tick" from "already missed".
func (o *Order) TicksLeft(now int64) int64 { return o.DeadlineTick - now }

// Expired reports whether the deadline has passed at tick now.
//
// Strictly greater: an order is NOT expired during the tick its deadline names,
// so a delivery landing exactly on deadlineTick still counts as on time. Off by
// one here silently turns the last-chance tick into a failure.
func (o *Order) Expired(now int64) bool { return now > o.DeadlineTick }

// OpenOrders returns orders whose STATE is still live — everything except DONE
// and EXPIRED.
//
// Note the distinction: this is state-based, NOT deadline-based. An order whose
// deadline has already passed but which the tick loop has not swept yet is
// still included, even though Order.Expired(now) reports true for it. Verified:
// forcing DeadlineTick to now-50 leaves the order in OpenOrders while Expired
// returns true.
//
// That window is one tick wide in normal play (spawnOrders then expireOrders in
// the same Advance), so callers gating on "how much work is outstanding" get the
// value the spawner sees. A caller asking "is this order still deliverable"
// must check Expired as well — an overdue-but-unswept order is not.
func (w *World) OpenOrders() []*Order {
	var out []*Order
	for _, o := range w.Orders {
		if !o.State.Terminal() {
			out = append(out, o)
		}
	}
	return out
}

// OrdersList returns every order, newest first, in a stable order.
//
// It copies rather than aliasing w.Orders because the tick loop appends to that
// slice: handing the live slice to a caller would let a read race with a spawn.
// Ties on CreatedTick break by id, so two orders raised on the same tick always
// list in the same order.
func (w *World) OrdersList() []*Order {
	out := make([]*Order, len(w.Orders))
	copy(out, w.Orders)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedTick != out[j].CreatedTick {
			return out[i].CreatedTick > out[j].CreatedTick
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ActiveOrderCount is the number of orders whose state is still live.
// See OpenOrders for why this is not "orders before their deadline".
func (w *World) ActiveOrderCount() int { return len(w.OpenOrders()) }

// Order returns the live order with the given id, not a copy. Mutating it
// changes the world; on a running session, do it inside Session.Do.
func (w *World) Order(id string) (*Order, bool) {
	for _, o := range w.Orders {
		if o.ID == id {
			return o, true
		}
	}
	return nil, false
}

// AssignOrder points an order at a car. Only PENDING orders can be assigned.
//
// Assignment refreshes the deadline. The spawn-time deadline assumes a free
// courier, but with one car an order often appears while the fleet is busy, so
// judging it against the moment it was raised punishes the player for the
// spawner's timing rather than for their driving. Once a courier commits, the
// clock restarts from a fresh route estimate. Orders still have a spawn
// deadline, so they age out if nobody ever claims them.
func (w *World) AssignOrder(orderID, carID string) error {
	o, ok := w.Order(orderID)
	if !ok {
		return fmt.Errorf("no order %q", orderID)
	}
	car, ok := w.Car(carID)
	if !ok {
		return fmt.Errorf("no car %q", carID)
	}
	switch o.State {
	case OrderDone, OrderExpired:
		return fmt.Errorf("order %q is already %s", orderID, o.State)
	case OrderPickedUp:
		return fmt.Errorf("order %q is already in %s", orderID, o.AssignedCar)
	}
	// An order belongs to one car at a time; reassigning back to PENDING is
	// done with UnassignOrder.
	if o.State == OrderAssigned && o.AssignedCar != carID {
		return fmt.Errorf("order %q is assigned to %s", orderID, o.AssignedCar)
	}
	if !car.SpaceFor(o.Boxes()) && car.ID != o.AssignedCar {
		return fmt.Errorf("%s cannot take %d boxes (%d/%d in use)",
			car.Name, o.Boxes(), car.Load(), car.Class.Capacity)
	}
	o.AssignedCar = carID
	o.State = OrderAssigned

	// Refresh the deadline from this car's actual position, so the window
	// measures the drive rather than the queue.
	//
	// This only ever EXTENDS. `fresh` is applied when it is later than the
	// current deadline, so assigning a car that already sits next to the
	// kitchen cannot pull in a deadline the player was already shown. Verified:
	// an order forced to +999 ticks still reads +999 after assignment. Do not
	// "fix" this into an unconditional assignment — shortening a quoted
	// deadline retroactively is the kind of thing players read as a bug.
	if k, ok := w.kitchenByID(o.KitchenID); ok {
		if cu, ok := w.customerByID(o.CustomerID); ok {
			fresh := w.Tick + w.estimateFrom(car.Pos, k.Pos, cu.Pos)
			if fresh > o.DeadlineTick {
				o.DeadlineTick = fresh
			}
		}
	}
	return nil
}

// UnassignOrder releases an ASSIGNED order back to PENDING and clears its car.
// Only ASSIGNED is accepted: a PICKED_UP order is already in a car's hold, and
// a pending or terminal order returns an error. The deadline and penalty are
// not reset, so the order keeps ageing from its original clock.
func (w *World) UnassignOrder(orderID string) error {
	o, ok := w.Order(orderID)
	if !ok {
		return fmt.Errorf("no order %q", orderID)
	}
	if o.State != OrderAssigned {
		return fmt.Errorf("order %q is %s, not assigned", orderID, o.State)
	}
	o.AssignedCar = ""
	o.State = OrderPending
	return nil
}

// resolveTileInteractions runs pickups and dropoffs for a car that has just
// stepped onto a tile. Called from the tick, after the move landed.
//
// Pickup is automatic: stepping onto the kitchen with the order assigned to
// you and space in the car collects it. Requiring a separate pickup call would
// double the API traffic for every delivery without adding a decision.
func (w *World) resolveTileInteractions(car *Car) {
	// Pickup.
	if k, ok := w.City.KitchenAt(car.Pos); ok {
		for _, o := range w.OpenOrders() {
			if o.State != OrderAssigned || o.AssignedCar != car.ID || o.KitchenID != k.ID {
				continue
			}
			// A car that cannot fit the whole order takes none of it. Partial
			// pickup was considered and rejected: it would let a scooter ferry
			// a forty-box order four boxes at a time, which turns the size gate
			// into a tedium gate and makes the truck pointless.
			if !car.SpaceFor(o.Boxes()) {
				continue
			}
			o.State = OrderPickedUp
			car.addCargo(o.ID, o.Boxes())
			w.emit(EventPickedUp, car,
				fmt.Sprintf("%s picked up %d× %s from %s", car.Name, o.Boxes(), w.itemLabel(o.Item), k.Name))
		}
	}

	// Dropoff.
	//
	// Every order for this customer is delivered in one pass: standing at the
	// address with three orders is one arrival, not three. The loop rebuilds
	// the cargo slice rather than mutating it in place, because removing while
	// ranging would skip the next order.
	if cu, ok := w.City.CustomerAt(car.Pos); ok {
		kept := car.Cargo[:0]
		for _, orderID := range car.Cargo {
			o, found := w.Order(orderID)
			if !found || o.CustomerID != cu.ID || o.State != OrderPickedUp {
				kept = append(kept, orderID)
				continue
			}
			car.cargoBoxes -= o.Boxes()
			w.completeOrder(car, o, cu)
		}
		car.Cargo = kept
		if car.cargoBoxes < 0 {
			car.cargoBoxes = 0
		}
	}
}

// completeOrder settles payment and counters for a delivered order.
func (w *World) completeOrder(car *Car, o *Order, cu Customer) {
	o.State = OrderDone
	o.DeliveredTick = w.Tick

	late := o.Expired(w.Tick)
	pay := o.Reward
	kind := CashDelivery
	if late {
		pay = int(float64(o.Reward) * w.ordersCfg.LatePayMultiplier)
		o.Late = true
		kind = CashLateDelivery
	}
	o.Paid = pay

	w.recordCash(kind, pay, car, o.ID,
		fmt.Sprintf("%s to %s", w.itemLabel(o.Item), cu.Name))
	w.Ledger.MoneyEarned += pay
	if late {
		w.Ledger.LateDeliveries++
		w.Ledger.OnTimeStreak = 0
		w.Ledger.Reputation -= w.ordersCfg.ReputationPerLate
	} else {
		w.Ledger.Deliveries++
		w.Ledger.OnTimeStreak++
		w.Ledger.Reputation += w.ordersCfg.ReputationPerDelivery
	}
	w.Ledger.DeliveriesByItem[o.Item]++
	w.clampReputation()

	// The cooldown starts again when the customer leaves, not only when the
	// order was raised. A slow delivery would otherwise leave the site eligible
	// the moment its order completes, so the same address could pop back on the
	// very next tick; now it always rests for its interval first.
	w.advanceCooldown(cu, o.Item)

	note := ""
	if late {
		note = " (late)"
	}
	w.emit(EventDelivered, car, fmt.Sprintf("%s delivered %s to %s +$%d%s",
		car.Name, w.itemLabel(o.Item), cu.Name, pay, note))
}

// clampReputation keeps reputation inside [0, max_reputation]. Without this a
// long winning streak pushes it past the scale's top, which then makes
// reputation-gated unlocks trivially satisfied and hides the real difficulty
// curve.
func (w *World) clampReputation() {
	if w.Ledger.Reputation > w.maxReputation {
		w.Ledger.Reputation = w.maxReputation
	}
	if w.Ledger.Reputation < 0 {
		w.Ledger.Reputation = 0
	}
}

// expireOrders marks overdue open orders EXPIRED and applies the penalty.
// Orders already picked up still expire: a customer does not wait forever
// because a courier is holding the box.
func (w *World) expireOrders() {
	for _, o := range w.Orders {
		if o.State.Terminal() {
			continue
		}
		if !o.Expired(w.Tick) {
			continue
		}
		// Diagnostics for the pacing harness: an order that expires while
		// still PENDING was never claimed (a spawn-rate problem); one that
		// expires ASSIGNED or PICKED_UP was too slow (a drive-time problem).
		switch o.State {
		case OrderPending:
			w.Ledger.ExpiredUnclaimed++
		case OrderAssigned:
			w.Ledger.ExpiredAssigned++
		case OrderPickedUp:
			w.Ledger.ExpiredInTransit++
		}
		// Still held by a car? Drop it from the cargo, and free its boxes.
		if o.State == OrderPickedUp {
			if car, ok := w.Car(o.AssignedCar); ok {
				car.removeCargo(o.ID, o.Boxes())
			}
		}
		o.State = OrderExpired
		var car *Car
		if o.AssignedCar != "" {
			car, _ = w.Car(o.AssignedCar)
		}
		w.recordCash(CashOrderFine, -o.Penalty, car, o.ID,
			fmt.Sprintf("%s expired", w.itemLabel(o.Item)))
		w.Ledger.ExpiredOrders++
		w.Ledger.OnTimeStreak = 0
		w.Ledger.Reputation -= w.ordersCfg.ExpirePenaltyReputation
		w.clampReputation()
		// An expired order also makes the customer leave, so it rests for its
		// interval before it can be seen again — the same rule as a delivery.
		if cu, ok := w.customerByID(o.CustomerID); ok {
			w.advanceCooldown(cu, o.Item)
		}
		w.emit(EventExpired, car, fmt.Sprintf("%s order expired (-$%d)", w.itemLabel(o.Item), o.Penalty))
	}
}

// itemLabel returns the display name of an item, falling back to its id.
func (w *World) itemLabel(item string) string {
	if name, ok := w.City.Items[item]; ok && name != "" {
		return name
	}
	return item
}

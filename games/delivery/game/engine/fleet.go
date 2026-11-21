package engine

import "fmt"

// Fleet running costs.
//
// The rule is: a car parked on a depot tile is free, and a car anywhere else
// costs money every UpkeepIntervalTicks. That is the whole mechanic, and the
// shape of it is a deliberate choice rather than a simplification.
//
// The obvious version -- "an idle car costs money" -- does not survive contact
// with the rest of the engine:
//
//   - There is no sell or scrap action, so a per-tick charge on an unsellable
//     asset turns one bad purchase into a permanent bleed. Parking is the exit,
//     and it already exists.
//   - CarState idles for reasons the player does not control: an empty board, a
//     full battery, a screen the player is still reading. Billing those is
//     billing the player for the game's pacing.
//   - An idle car is often waiting for work the player cannot control, such as
//     a customer cooldown or a queue the spawner has not filled yet. Billing
//     that tick would be billing the player for the game's pacing, not for a
//     decision. Parking against a depot is the honest exit.
//
// Depots are the natural home for this because parking there is always free;
// fleet size itself is money-gated, so the depot is not a separate limit.
//
// THE RATE IS A MEASURED CLIFF, NOT A FREE KNOB. Upkeep is a fixed cost per car
// against variable income, and this game has no credit, loan or sell action to
// absorb a bad patch. The consequence, measured over 8 seeds x 3000 ticks with
// cmd/drbrain (see the table in README §"Fleet running cost"):
//
//	120 ticks/1 per box (0.008 money per car-tick)  mean 222.6 deliveries, no seed collapses
//	 40 ticks/1 per box (0.021)                     mean 210.5, seed 6 collapses to rep 0.00
//	 40 ticks/4 per box (0.083)                     mean 207.9, seed 6 collapses to rep 0.00
//
// The transition is bistable rather than gradual: seed 6 finishes at 238
// deliveries / rep 4.81 at 45 ticks and at 139 / rep 0.00 at 40, deterministically.
// Money stalls near $5k around tick 800, the next build becomes unaffordable,
// and the run never recovers.
//
// Two consequences worth stating plainly, because both were learned the hard way:
//
//   - At the shipped rate this mechanic is close to inert (-1.1 deliveries vs
//     off). That is the deliberate trade: "occasionally destroys a run" is worse
//     than "barely noticeable".
//   - A literal idle-only variant (charge nothing while carrying or assigned)
//     does NOT remove the cliff — 40/2 idle-only still collapses seed 6. The
//     rate is what breaks a run, not the choice of which cars are billed.
//
// TestShippedUpkeepRateDoesNotCollapseTheCanarySeed in upkeep_pacing_test.go
// guards this. Do not raise the rate without re-running seed 6.

// EventUpkeep is emitted when upkeep could not be paid in full.
const EventUpkeep = "fleet_upkeep"

// upkeepEnabled reports whether this world charges for its fleet at all.
//
// The map must state its fleet choice; an interval of 0 disables the feature
// explicitly, so a quiet map keeps the pre-upkeep economy.
func (w *World) upkeepEnabled() bool { return w.fleet.UpkeepIntervalTicks > 0 }

// Parked reports whether a car is standing on a depot tile and therefore costs
// nothing to keep.
//
// This is deliberately terrain-based rather than a stored "parked" flag: a car
// driving through the depot tile is parked for that tick (free), and a car that
// leaves resumes paying. No flag means no state that can disagree with position.
func (w *World) Parked(car *Car) bool {
	t, ok := w.City.At(car.Pos)
	return ok && t == DepotTile
}

// UpkeepCost is what this world charges per upkeep interval for one car: zero
// when it is parked or the feature is off, otherwise the class's rate.
//
// The class rate is Capacity * fleet.UpkeepPerBoxPerInterval, floored at 1. It is
// scaled by hold size rather than hand-tuned per class because the revenue a car
// can generate scales with how much it carries, so a rate that ignores capacity
// makes either the scooter pointless or the truck free money. See
// CarClassConfig.UpkeepPerInterval to override one class explicitly.
func (w *World) UpkeepCost(car *Car) int {
	if !w.upkeepEnabled() || w.Parked(car) {
		return 0
	}
	if rate := car.Class.UpkeepPerInterval; rate > 0 {
		return rate
	}
	if car.Class.Capacity <= 0 {
		return w.fleet.DefaultUpkeepPerInterval
	}
	rate := car.Class.Capacity * w.fleet.UpkeepPerBoxPerInterval / w.fleet.UpkeepBoxDivisor
	if rate < 1 {
		rate = 1
	}
	return rate
}

// FleetUpkeep is the total upkeep this world charges on the current tick for
// the whole fleet, without charging it. A UI uses it to show the running cost
// before it lands.
func (w *World) FleetUpkeep() int {
	total := 0
	for _, c := range w.Cars {
		total += w.UpkeepCost(c)
	}
	return total
}

// chargeUpkeep bills the fleet once per interval.
//
// It runs near the end of the tick, after movement and charging, so a car that
// parks during this tick is not charged for it: the player's action lands first
// and the bill follows. That ordering is the difference between "parking saves
// money" being true and being a lie at the tick boundary.
//
// Money clamps at zero, matching expireOrders. A charge that cannot be paid in
// full emits exactly one event rather than one per car, because the event log is
// a 500-entry ring and a broke fleet would otherwise flush the whole history of
// the session every interval.
func (w *World) chargeUpkeep() {
	if !w.upkeepEnabled() {
		return
	}
	// Interval boundaries are counted on the tick just completed. Advance
	// increments w.Tick at the END, so while this runs w.Tick is the zero-based
	// index of the tick being processed: the first bill therefore lands on the
	// last call of the first interval (w.Tick == interval-1), not one tick late.
	// Getting this off by one is invisible in play but doubles the effective
	// interval, so it is asserted in the tests.
	if (w.Tick+1)%int64(w.fleet.UpkeepIntervalTicks) != 0 {
		return
	}

	due := w.FleetUpkeep()
	if due <= 0 {
		return
	}

	// Bill each car separately so the statement can tag the cost to the vehicle
	// that incurred it. The totals and the clamp are unchanged: the balance is
	// consumed in car order, and a shortfall still emits one event rather than
	// one per car.
	paid := 0
	for _, car := range w.Cars {
		cost := w.UpkeepCost(car)
		if cost <= 0 {
			continue
		}
		applied := w.recordCash(CashUpkeep, -cost, car, "", "fleet upkeep")
		paid += -applied
	}
	w.Ledger.UpkeepPaid += paid
	if paid < due {
		w.emit(EventUpkeep, nil,
			fmt.Sprintf("fleet upkeep $%d exceeded balance (paid $%d of $%d); park cars at a depot to stop the bleed", due, paid, due))
	}
}

// TowFee is what towing this car would cost, given the depot it would go to.
//
// The fee is priced by route distance so a cross-city recovery is not the same
// deal as a nudge from the next block, and floored so the free-teleport exploit
// (move a car one tile per tow) is never cheaper than just driving.
func (w *World) TowFee(car *Car, dest Position) int {
	if w.fleet.TowFeePerTile <= 0 && w.fleet.TowFeeMin <= 0 {
		return 0
	}
	fee := w.towDistance(car.Pos, dest) * w.fleet.TowFeePerTile
	if fee < w.fleet.TowFeeMin {
		fee = w.fleet.TowFeeMin
	}
	return fee
}

// towDistance is the distance a tow is priced on, and the distance used to pick
// which depot is nearest.
//
// It is the ROUTE the car would have had to drive, not City.Distance, because
// the fee is meant to price the recovery the player is buying. Every other
// player-facing distance in the engine (deadlines, rewards, nearest-kitchen, the
// harness bot) already goes through routeDistance; using the closure-blind
// primitive here undercharged a tow that had to be routed around street works.
// Measured case on the shipped map with a closure at [(7,3)(8,3)(9,3)]: a car at
// (3,16) is 13 tiles away as the crow flies but 23 by road, so the old fee was
// $26 against a fair $46.
//
// The fallback matters and is deliberate: routeDistance returns -1 when no route
// exists at all — a car sealed in by works, which is exactly when a player tows.
// Charging $0 for that would make the worst case the cheapest, so an unreachable
// car is priced on the straight-line distance instead. A tow is a teleport and
// does not need a route; the fee is a fairness heuristic, not a pathfinding
// proof, and it must never be zero just because the map is temporarily broken.
func (w *World) towDistance(from, to Position) int {
	if d := w.routeDistance(from, to); d > 0 {
		return d
	}
	if d := w.City.Distance(from, to); d > 0 {
		return d
	}
	return 0
}

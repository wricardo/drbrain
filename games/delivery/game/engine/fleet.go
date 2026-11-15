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
//   - It would fight targetActive, which counts every non-stranded car and
//     therefore already REWARDS owning a car you never drive. Parking against a
//     depot is the honest mirror of that subsidy.
//
// Depots are the natural home for this because DepotConfig.Capacity otherwise
// only gates purchases. On the shipped map the depot holds 4 and grows to 8 with
// b06_depot_yard, so a fleet larger than the yard cannot all be mothballed. That
// constraint is what makes the choice real.

// EventUpkeep is emitted when upkeep could not be paid in full.
const EventUpkeep = "fleet_upkeep"

// upkeepEnabled reports whether this world charges for its fleet at all.
//
// The zero value of the config block disables the feature, so a map that omits
// `fleet` keeps the pre-upkeep economy and an existing save is not silently
// repriced the moment this code ships.
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
	rate := car.Class.Capacity * w.fleet.UpkeepPerBoxPerInterval / 4
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

	paid := due
	if w.Ledger.Money < due {
		paid = w.Ledger.Money
	}
	w.Ledger.Money -= paid
	w.Ledger.UpkeepPaid += paid
	if w.Ledger.Money < 0 {
		w.Ledger.Money = 0
	}
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
	dist := w.City.Distance(car.Pos, dest)
	if dist < 0 {
		dist = 0
	}
	fee := dist * w.fleet.TowFeePerTile
	if fee < w.fleet.TowFeeMin {
		fee = w.fleet.TowFeeMin
	}
	return fee
}

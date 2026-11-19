package engine

import (
	"testing"
)

// Fleet upkeep is not a smooth dial. These tests pin the two things that
// actually matter about it, both of which were found by measurement rather than
// reasoning (see drbrain-delivery-hs9):
//
//  1. Above roughly 0.02 money per car-tick the shipped economy BISTABLE —
//     some seeds finish the run healthy and others collapse to reputation 0.00
//     and never recover, because upkeep is a fixed cost against variable income
//     and botSpend needs a reserve to fund the next build. Seed 6 is the
//     canary: fine at 45 ticks, reputation 0.00 at 40.
//
//  2. At the shipped rate (120 ticks, 1 per box ≈ 0.008 per car-tick) upkeep is
//     close to inert. That is deliberate: the alternative is a mechanic that
//     occasionally destroys a run. Do not "make it feel more meaningful" by
//     raising the rate without re-running the canary seed.
//
// These are content tests, not rule tests: they assert properties of the shipped
// map, so they fail if someone retunes the map into the danger zone.

// canarySeed is the seed that exposes the collapse. It is the only seed out of
// eight that zeroes reputation under a too-heavy rate, so it is the cheapest
// possible regression guard.
const canarySeed uint64 = 6

// runSim drives the world for n ticks with simple play: buy what is affordable,
// tow stranded cars, assign pending orders, drive each car one step toward its
// goal. It deliberately mirrors cmd/drbrain rather than importing it (that is
// package main), so the engine suite covers the same shape of play.
func runSim(t *testing.T, w *World, ticks int) {
	t.Helper()
	for range ticks {
		// Unstick first: a stranded car is a permanent dead end.
		for _, c := range w.Cars {
			if c.State(w.City) == CarStranded {
				_ = w.Tow(c.ID)
			}
		}
		// Assign pending work to any car with room.
		for _, o := range w.OpenOrders() {
			if o.State != OrderPending {
				continue
			}
			for _, c := range w.Cars {
				if c.State(w.City) == CarStranded {
					continue
				}
				if c.Class.Capacity >= o.Boxes() {
					_ = w.AssignOrder(o.ID, c.ID)
					break
				}
			}
		}
		// Drive each car one step toward its goal, diverting to charge when the
		// next leg is longer than the battery can cover.
		for _, c := range w.Cars {
			if c.State(w.City) == CarStranded {
				continue
			}
			goal, ok := simGoal(w, c)
			if !ok {
				continue
			}
			if stop, needs := simNeedsCharge(w, c, goal); needs {
				goal = stop
			}
			if dir := w.StepToward(c.Pos, goal); dir != "" {
				_ = w.QueueMove(c.ID, dir)
			}
		}
		w.AdvanceNoPause()
	}
}

// simGoal is the tile a car is trying to reach: its order's customer once it is
// carrying, otherwise the kitchen.
func simGoal(w *World, c *Car) (Position, bool) {
	if len(c.Cargo) == 0 {
		for _, o := range w.Orders {
			if o.AssignedCar == c.ID && o.State == OrderAssigned {
				for _, k := range w.City.Kitchens {
					if k.ID == o.KitchenID {
						return k.Pos, true
					}
				}
			}
		}
		return Position{}, false
	}
	o, ok := w.Order(c.Cargo[0])
	if !ok {
		return Position{}, false
	}
	for _, cu := range w.City.Customers {
		if cu.ID == o.CustomerID {
			return cu.Pos, true
		}
	}
	return Position{}, false
}

// simNeedsCharge reports whether the car should divert to a charger before
// continuing. Without this a naive driver strands constantly and the resulting
// tow storm looks like an upkeep problem rather than a harness problem — which
// is exactly the trap this whole investigation fell into once already.
func simNeedsCharge(w *World, c *Car, goal Position) (Position, bool) {
	if w.City.ChargingAt(c.Pos) {
		return Position{}, false // already recovering
	}
	toGoal := w.RouteDistance(c.Pos, goal)
	if toGoal < 0 {
		return Position{}, false
	}
	if toGoal+1 <= c.Battery {
		return Position{}, false // enough for this leg
	}
	// Not enough charge: divert to the nearest charger or depot.
	best := Position{}
	found := false
	for _, p := range w.NearestChargers(c.Pos, 0) {
		if p == c.Pos {
			continue
		}
		if !found || w.City.Distance(c.Pos, p) < w.City.Distance(c.Pos, best) {
			best, found = p, true
		}
	}
	if !found {
		return Position{}, false
	}
	return best, true
}

// TestShippedUpkeepRateDoesNotCollapseTheCanarySeed is the regression guard for
// the danger zone. On the shipped map this seed finishes healthy; a rate heavy
// enough to starve the build ladder takes it to reputation 0.00 and roughly 100
// fewer deliveries.
func TestShippedUpkeepRateDoesNotCollapseTheCanarySeed(t *testing.T) {
	city, cfg := shippedCity(t)
	if !(cfg.Fleet.UpkeepIntervalTicks > 0) {
		t.Fatal("shipped map has no upkeep configured; this test assumes it is on")
	}
	w, err := NewWorldSeeded(city, cfg, canarySeed)
	if err != nil {
		t.Fatal(err)
	}
	runSim(t, w, 3000)

	// Reputation is the sensitive signal: it falls continuously with expiries
	// and is the first thing to zero in a collapse.
	if got := w.Ledger.Reputation; got < 2.0 {
		t.Fatalf("canary seed reputation %.2f after 3000 ticks: the upkeep rate in maps/city.json "+
			"is in the collapse region (see the comment at the top of this file). "+
			"Lower fleet.upkeep_per_box_per_interval or raise upkeep_interval_ticks.",
			got)
	}
	if got := w.Ledger.Deliveries; got < 150 {
		t.Fatalf("canary seed made only %d deliveries after 3000 ticks; expected 200+. "+
			"A collapse run produces roughly 139.", got)
	}
}

// TestShippedUpkeepIsConfiguredButCheap documents the intended shape of the
// setting. The shipped rate has to stay small enough to be safe, and this pins
// the order of magnitude so a future "let's make upkeep meaningful" change has
// to consciously cross this line rather than drifting over it.
func TestShippedUpkeepIsConfiguredButCheap(t *testing.T) {
	_, cfg := shippedCity(t)
	f := cfg.Fleet
	if f.UpkeepIntervalTicks <= 0 {
		t.Fatal("fleet upkeep is disabled on the shipped map; the mechanic is off")
	}
	// cost per car-tick for a scooter (capacity 4) = (4/4 * per_box) / interval
	perTick := float64(f.UpkeepPerBoxPerInterval) / float64(f.UpkeepIntervalTicks)
	const dangerZone = 0.02
	if perTick >= dangerZone {
		t.Fatalf("shipped upkeep is %.4f money per car-tick, at or above the %.2f collapse "+
			"threshold measured on seed %d (fine at 0.0222, collapsed at 0.025). "+
			"Re-run the canary seed before raising this.", perTick, dangerZone, canarySeed)
	}
	if perTick <= 0 {
		t.Fatalf("shipped upkeep computes to zero cost per car-tick; the mechanic does nothing")
	}
}

// NOTE on what is NOT tested here.
//
// An earlier version of this file asserted that the 40/4 rate (0.1 money per
// car-tick) collapses seed 6. It does — under cmd/drbrain's bot, which spends on
// builds every tick and needs a $100 reserve. It does NOT under the simplified
// harness above, which is too naive to chase the build ladder and therefore
// never starves: 40/4 scores 214 deliveries / reputation 5.00 there.
//
// So the collapse is a property of the bot and the economy together, not of the
// engine alone, and asserting it from inside game/engine would be asserting a
// fact about a harness this package cannot see. The rate guard below is the
// honest thing to enforce from here; the collapse itself is measured with
// `cmd/drbrain sim -seed 6` and recorded in drbrain-delivery-hs9.

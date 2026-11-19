package engine

import "testing"

// Tow fees are priced on the ROUTE, not the straight line. See
// drbrain-delivery-x43: pricing on City.Distance undercharged a tow that had to
// be routed around street works, which is the case a player most wants to tow.
//
// The tiny map is a 5x5 open road interior with the depot as a three-tile row
// at (1,1),(2,1),(3,1), and the interior rows y=2..5 run x=1..5.

// towTestWorld builds the tiny map with a tow fee configured.
//
// PerTile is deliberately large relative to the map so the DISTANCE term
// dominates and the TowFeeMin floor cannot mask a wrong distance. With the
// shipped rate (2/tile, $25 floor) a 4-to-6 tile difference is entirely swallowed
// by the floor, which made an earlier version of these tests vacuous: they passed
// with the bug present.
func towTestWorld(t *testing.T) *World {
	t.Helper()
	w := tinyWorld(t)
	w.fleet = FleetConfig{
		TowFeePerTile: 10,
		TowFeeMin:     5,
	}
	return w
}

// TestTowFeeUsesRouteDistanceNotStraightLine is the regression for x43.
//
// A wall of closures forces the road route to be longer than the straight-line
// distance. The fee must follow the road, because that is the recovery the player
// is buying.
func TestTowFeeUsesRouteDistanceNotStraightLine(t *testing.T) {
	w := towTestWorld(t)
	car := w.Cars[0]

	from, to := Position{1, 5}, Position{1, 1}
	straight := w.City.Distance(from, to)
	if straight < 0 {
		t.Fatal("test tiles are not connected without closures")
	}

	// Closing x=1 in rows 2..4 forces a car at (1,5) to escape east, run north
	// along x=3, and come back west along the depot row: 6 tiles instead of 4.
	// (Closing fewer rows leaves a gap through the depot row; closing all of
	// them would seal the destination.)
	w.closures = []Closure{{
		ID: "wall", StartTick: 0, EndTick: 100000,
		Tiles: []Position{{1, 2}, {1, 3}, {1, 4}},
	}}
	car.Pos = from

	routed := w.routeDistance(from, to)
	if routed <= straight {
		t.Fatalf("test setup failed to lengthen the route: straight %d, routed %d", straight, routed)
	}

	fee := w.TowFee(car, to)
	wantRouted := routed * w.fleet.TowFeePerTile
	wantStraight := straight * w.fleet.TowFeePerTile

	// Guard against the test being vacuous: if both fees sat under the floor,
	// the floor would mask the bug and this test would pass either way.
	if wantRouted <= w.fleet.TowFeeMin || wantStraight <= w.fleet.TowFeeMin {
		t.Fatalf("test setup is useless: the floor ($%d) masks both candidate fees (%d and %d)",
			w.fleet.TowFeeMin, wantStraight, wantRouted)
	}
	if fee == wantStraight {
		t.Fatalf("tow fee %d matches the straight-line distance (%d tiles); it must price the "+
			"route (%d tiles), the same way deadlines and rewards do", fee, straight, routed)
	}
	if fee != wantRouted {
		t.Fatalf("tow fee = %d, want %d (%d routed tiles x %d)",
			fee, wantRouted, routed, w.fleet.TowFeePerTile)
	}
}

// TestTowFeeFallsBackWhenNoRouteExists pins the deliberate exception: a car
// sealed in by works is exactly the car that needs a tow, so it must not be towed
// for free. The fee falls back to the straight-line distance rather than 0.
func TestTowFeeFallsBackWhenNoRouteExists(t *testing.T) {
	w := towTestWorld(t)
	car := w.Cars[0]

	// Seal a single road tile in with closures on all four of its neighbours.
	car.Pos = Position{3, 3}
	w.closures = []Closure{{
		ID: "seal", StartTick: 0, EndTick: 100000,
		Tiles: []Position{{2, 3}, {4, 3}, {3, 2}, {3, 4}},
	}}

	if d := w.routeDistance(car.Pos, Position{1, 1}); d != -1 {
		t.Fatalf("test setup: expected the car to be sealed in, routeDistance = %d", d)
	}
	if fee := w.TowFee(car, Position{1, 1}); fee <= 0 {
		t.Fatalf("tow fee = %d for a sealed-in car; a tow must never be free just because "+
			"the map is temporarily impassable", fee)
	}
}

// TestNoClosuresKeepsTheCheapFee proves the fix does not inflate the ordinary
// case: with no street works the route and the straight line are the same, so
// fees are unchanged from before x43.
func TestNoClosuresKeepsTheCheapFee(t *testing.T) {
	w := towTestWorld(t)
	car := w.Cars[0]
	car.Pos = Position{5, 5}
	to := Position{1, 1}

	straight := w.City.Distance(car.Pos, to)
	if straight*w.fleet.TowFeePerTile <= w.fleet.TowFeeMin {
		t.Fatalf("test setup is useless: the floor would mask the distance term")
	}
	if fee, want := w.TowFee(car, to), straight*w.fleet.TowFeePerTile; fee != want {
		t.Fatalf("tow fee = %d, want %d with no closures active", fee, want)
	}
}

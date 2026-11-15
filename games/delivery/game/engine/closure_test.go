package engine

import (
	"testing"
)

// closureWorld builds the shipped city with closures forced on, for tests that
// need street works without waiting for the pacing table.
func closureWorld(t *testing.T) (*World, *CityConfig) {
	t.Helper()
	city, cfg := shippedCity(t)
	cfg.Closures.Enabled = true
	cfg.Closures.FromChapter = 0
	if cfg.Closures.EveryTicks <= 0 {
		cfg.Closures.EveryTicks = 1
	}
	if cfg.Closures.DurationTicks <= 0 {
		cfg.Closures.DurationTicks = 50
	}
	if cfg.Closures.SegmentTiles <= 0 {
		cfg.Closures.SegmentTiles = 3
	}
	w, err := NewWorldSeeded(city, cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	w.Ledger.Money = 100000
	return w, cfg
}

// TestClosureNeverDisconnectsAnything is the safety rule the whole mechanic
// rests on. Two blocks of street work can cut a corner off between them; a
// closure that does that is never scheduled, however convenient the dice
// roll.
//
// This is the guard whose absence sold 72 undeliverable orders out of 175 in a
// 3000-tick probe with the East Bridge shut.
func TestClosureNeverDisconnectsAnything(t *testing.T) {
	w, cfg := closureWorld(t)
	// Reveal everything so the candidate pool is the whole map, which is the
	// hardest case: more streets means more chances to cut a corner off.
	w.City.RevealAll()
	w.Ledger.BuildsCompleted = 99 // past every FromChapter gate

	depot := w.City.Depots[0].Pos
	before := w.City.Distances(depot)

	scheduled := 0
	for tick := int64(0); tick < 4000; tick++ {
		w.Tick = tick
		c, ok := w.closureAt(tick)
		if !ok {
			continue
		}
		scheduled++

		// Nothing that was reachable may become unreachable, except the
		// closed tiles themselves.
		blocked := map[Position]bool{}
		for _, p := range c.Tiles {
			if !w.City.Walkable(p) {
				t.Fatalf("closure %s covers an unwalkable tile %v", c.ID, p)
			}
			blocked[p] = true
		}
		after := w.City.DistancesAvoiding(depot, blocked)
		for p := range before {
			if blocked[p] {
				continue
			}
			if _, ok := after[p]; !ok {
				t.Fatalf("closure %s at tick %d cut %v off from the depot",
					c.ID, tick, p)
			}
		}
	}
	if scheduled == 0 {
		t.Fatal("no closures were ever scheduled: the test proved nothing")
	}
	_ = cfg
}

// TestClosureCandidatesAvoidEntitiesAndChargers pins the two exclusions that
// keep a closure readable as traffic rather than as a broken map.
func TestClosureCandidatesAvoidEntitiesAndChargers(t *testing.T) {
	w, _ := closureWorld(t)
	w.City.RevealAll()

	candidates := w.closureCandidates(3)
	if len(candidates) == 0 {
		t.Fatal("no closure candidates on the shipped map")
	}
	for _, tiles := range candidates {
		for _, p := range tiles {
			if w.City.hasEntityAt(p) {
				t.Fatalf("candidate covers entity tile %v", p)
			}
			if w.City.ChargingAt(p) {
				t.Fatalf("candidate covers charging tile %v", p)
			}
			if !w.City.Walkable(p) {
				t.Fatalf("candidate covers unwalkable tile %v", p)
			}
		}
	}
}

// TestClosureBlocksMovementButNotEscape is the "not a wall" rule: a car
// standing on a closed tile may drive off it, but may not drive back on.
func TestClosureBlocksMovementButNotEscape(t *testing.T) {
	w, _ := closureWorld(t)

	car := w.Cars[0]
	// Find a walkable tile with a walkable neighbour, close both, and stand
	// the car on the first one.
	var a, b Position
	found := false
	for y := range w.City.GridSize {
		if found {
			break
		}
		for x := range w.City.GridSize {
			p := Position{x, y}
			if !w.City.Walkable(p) || w.City.hasEntityAt(p) {
				continue
			}
			for _, d := range AllDirections {
				n, ok := p.Neighbour(d)
				if ok && w.City.Walkable(n) && !w.City.ChargingAt(n) {
					a, b, found = p, n, true
					break
				}
			}
			if found {
				break
			}
		}
	}
	if !found {
		t.Fatal("no usable pair of walkable tiles")
	}

	w.closures = []Closure{{
		// Only `a` is closed. The car starts parked on it, which is the
		// situation a closure starting under a parked car creates.
		ID: "test", Tiles: []Position{a}, StartTick: 0, EndTick: 1000,
	}}
	w.Tick = 1000 - 1

	// Standing on a closed tile, the car may leave.
	car.Pos = a
	dir := directionBetween(a, b)
	if dir == "" {
		t.Fatal("tiles are not neighbours")
	}
	_ = w.QueueMove(car.ID, dir)
	w.advanceCar(car)
	if car.Pos != b {
		t.Fatalf("car could not leave a closed tile: pos %v, reject %q", car.Pos, car.LastReject)
	}

	// Driving back on is refused, with a reason the player can read.
	back := directionBetween(b, a)
	_ = w.QueueMove(car.ID, back)
	w.advanceCar(car)
	if car.Pos != b {
		t.Fatalf("car entered a closed tile: pos %v", car.Pos)
	}
	if car.LastReject != RejectClosed {
		t.Fatalf("reject = %q, want %q", car.LastReject, RejectClosed)
	}
}

// TestClosureRoutingMatchesBlockedGeometry proves the route estimate goes
// around a closure, so a player is quoted a deadline for the drive they will
// actually make.
func TestClosureRoutingMatchesBlockedGeometry(t *testing.T) {
	w, _ := closureWorld(t)
	w.City.RevealAll()

	depot := w.City.Depots[0].Pos
	// Take the longest detour candidate available and check the closure
	// lengthens at least one route. A closure that changes nothing is
	// pointless drama, so the shipped geometry should always yield one.
	var best []Position
	bestGain := 0
	for _, tiles := range w.closureCandidates(3) {
		blocked := map[Position]bool{}
		for _, p := range tiles {
			blocked[p] = true
		}
		gain := 0
		for y := range w.City.GridSize {
			for x := range w.City.GridSize {
				p := Position{x, y}
				if _, ok := w.City.Distances(depot)[p]; !ok || blocked[p] {
					continue
				}
				before := w.City.Distance(depot, p)
				after := w.City.DistanceAvoiding(depot, p, func(q Position) bool { return blocked[q] })
				if after < 0 {
					t.Fatalf("candidate %v disconnects %v", tiles, p)
				}
				if after-before > gain {
					gain = after - before
				}
			}
		}
		if gain > bestGain {
			best, bestGain = tiles, gain
		}
	}
	if bestGain == 0 {
		t.Skip("no closure on this map lengthens any route")
	}

	w.closures = []Closure{{ID: "t", Tiles: best, StartTick: 0, EndTick: 10000}}
	w.Tick = 1

	// Find a tile whose route the closure lengthened, and prove the engine's
	// own distance agrees with the blocked geometry.
	blocked := map[Position]bool{}
	for _, p := range best {
		blocked[p] = true
	}
	checked := 0
	for y := range w.City.GridSize {
		for x := range w.City.GridSize {
			p := Position{x, y}
			if blocked[p] || !w.City.Walkable(p) {
				continue
			}
			want := w.City.DistanceAvoiding(depot, p,
				func(q Position) bool { return w.ClosedAt(q) })
			if got := w.routeDistance(depot, p); got != want {
				t.Fatalf("routeDistance(%v,%v) = %d, want %d", depot, p, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no tiles checked")
	}
}

// TestClosuresAreDerivedNotStored is the save-compatibility rule. A closure is
// a pure function of (seed, tick, chapter), so two worlds with the same seed
// must schedule the same closure on the same tick with no state carried over.
func TestClosuresAreDerivedNotStored(t *testing.T) {
	a, _ := closureWorld(t)
	b, _ := closureWorld(t)

	var found int64 = -1
	for tick := int64(0); tick < 2000; tick++ {
		if _, ok := a.closureAt(tick); ok {
			found = tick
			break
		}
	}
	if found < 0 {
		t.Fatal("no closure in the first 2000 ticks")
	}

	ca, okA := a.closureAt(found)
	cb, okB := b.closureAt(found)
	if !okA || !okB {
		t.Fatalf("closureAt(%d): %v / %v", found, okA, okB)
	}
	if len(ca.Tiles) != len(cb.Tiles) {
		t.Fatalf("same seed produced different closures: %v vs %v", ca.Tiles, cb.Tiles)
	}
	for i := range ca.Tiles {
		if ca.Tiles[i] != cb.Tiles[i] {
			t.Fatalf("same seed produced different tiles: %v vs %v", ca.Tiles, cb.Tiles)
		}
	}
	if ca.Reason != cb.Reason {
		t.Errorf("reason differs: %q vs %q", ca.Reason, cb.Reason)
	}

	// And it must be a function of the tick alone: asking twice, or asking
	// out of order, does not change the answer.
	again, _ := a.closureAt(found)
	if again.ID != ca.ID || len(again.Tiles) != len(ca.Tiles) {
		t.Error("closureAt is not a pure function of the tick")
	}
}

// TestClosuresDoNotStartBeforeTheirChapter keeps the early game quiet. A
// player with one scooter and no money has no interesting answer to a detour.
func TestClosuresDoNotStartBeforeTheirChapter(t *testing.T) {
	city, cfg := shippedCity(t)
	cfg.Closures.Enabled = true
	cfg.Closures.FromChapter = 2
	cfg.Closures.EveryTicks = 1
	cfg.Closures.DurationTicks = 10
	w, err := NewWorldSeeded(city, cfg, 3)
	if err != nil {
		t.Fatal(err)
	}

	w.Ledger.BuildsCompleted = 0
	if _, ok := w.closureAt(1); ok {
		t.Fatal("closure started at chapter 0 with FromChapter 2")
	}
	w.Ledger.BuildsCompleted = 1
	if _, ok := w.closureAt(1); ok {
		t.Fatal("closure started at chapter 1 with FromChapter 2")
	}
	w.Ledger.BuildsCompleted = 2
	// EveryTicks is 1, so any tick past the seeded phase qualifies.
	started := false
	for tick := int64(0); tick < 50 && !started; tick++ {
		if _, ok := w.closureAt(tick); ok {
			started = true
		}
	}
	if !started {
		t.Fatal("no closure once the chapter gate opened")
	}
}

// TestClosureRespectsConcurrencyCap proves the cap is enforced. Two live
// closures are not provably safe the way one is, so the cap is a guard rail
// rather than a tuning knob.
func TestClosureRespectsConcurrencyCap(t *testing.T) {
	city, cfg := shippedCity(t)
	cfg.Closures.Enabled = true
	cfg.Closures.FromChapter = 0
	cfg.Closures.EveryTicks = 1
	cfg.Closures.DurationTicks = 500
	cfg.Closures.MaxConcurrent = 1
	w, err := NewWorldSeeded(city, cfg, 11)
	if err != nil {
		t.Fatal(err)
	}
	w.City.RevealAll()
	w.Ledger.BuildsCompleted = 99

	for range 400 {
		w.AdvanceNoPause()
	}
	if got := len(w.ActiveClosures()); got > 1 {
		t.Fatalf("%d concurrent closures, cap was 1", got)
	}
}

// TestClosedTileIsNotServable proves the spawner stops selling work for a
// customer standing inside a closure.
func TestClosedTileIsNotServable(t *testing.T) {
	w, _ := closureWorld(t)
	w.City.RevealAll()

	var anna Customer
	for _, cu := range w.City.Customers {
		if cu.ID == "c_anna" {
			anna = cu
		}
	}
	if anna.ID == "" {
		t.Fatal("shipped city has no c_anna")
	}
	if !w.customerIsServable(anna) {
		t.Fatal("customer should be servable before any closure")
	}

	w.closures = []Closure{{
		ID: "t", Tiles: []Position{anna.Pos}, StartTick: 0, EndTick: 10000,
	}}
	w.Tick = 1
	if w.customerIsServable(anna) {
		t.Fatal("customer inside a closure reported as servable")
	}
}

// TestClosureGuardSeedsFromDepotsOnly is the regression test for a closure that
// severed the harbour from the only depot.
//
// The guard used to seed its BFS from depots AND chargers, copying
// City.StrandedEntities. That is right for a build -- a build may legitimately
// create a charger on the far side -- and wrong for a closure, because a car
// starts at a depot and cannot teleport to a charger. Once b04_harbour_gate adds
// ch_harbour_gate, the harbour carries its own seed, so the guard accepted a
// closure across the harbour road that left 59 tiles, k_sushi, c_docks,
// c_lighthouse and 5 of 8 cars unreachable from the only depot.
//
// TestClosureNeverDisconnectsAnything does not catch this: it reveals the map
// but never builds anything, so the harbour has no charger to seed from.
func TestClosureGuardSeedsFromDepotsOnly(t *testing.T) {
	w, _ := closureWorld(t)
	// Build the whole ladder, so every charger the map can grant exists. This is
	// the state a late-game player is in when most closures fire.
	w.Ledger.Money = 1_000_000
	for _, b := range w.Builds() {
		w.permitted[b.ID] = true
	}
	for i := range w.unlocks {
		w.unlocks[i].Earned = true
	}
	for _, b := range w.Builds() {
		if _, err := w.Build(b.ID); err != nil {
			t.Fatalf("build %s: %v", b.ID, err)
		}
	}

	// The harbour road bottleneck, exactly as the pacing table chose at tick
	// 5096 from the default seed.
	tiles := []Position{{15, 2}, {15, 3}, {15, 4}}
	if w.closureIsSafe(tiles) {
		t.Fatalf("guard accepted a closure that severs the harbour from the only depot: %v", tiles)
	}

	// And the reason it must reject: customers and a kitchen lose their route
	// to the fleet, not merely "some tile became unreachable".
	blocked := map[Position]bool{}
	for _, p := range tiles {
		blocked[p] = true
	}
	reach := w.reachFromSeeds(blocked)
	for _, id := range []string{"c_docks", "c_lighthouse"} {
		for _, cu := range w.City.Customers {
			if cu.ID == id && reach[cu.Pos] {
				t.Errorf("%s still reachable from the depot through a closed harbour road", id)
			}
		}
	}
}

// TestClosureGuardStillAcceptsTheWholeLadder guards the other direction: a
// tighter reachability rule must not make progression impossible. Every build
// in the ladder has to remain legal, in order, with the depot-only rule.
func TestClosureGuardStillAcceptsTheWholeLadder(t *testing.T) {
	w, cfg := closureWorld(t)
	w.Ledger.Money = 1_000_000
	for _, b := range w.Builds() {
		w.permitted[b.ID] = true
	}
	for i := range w.unlocks {
		w.unlocks[i].Earned = true
	}

	// Each build must be buildable in the authored order.
	for _, b := range cfg.Builds {
		if _, err := w.Build(b.ID); err != nil {
			t.Fatalf("build %s rejected under the depot-only guard: %v", b.ID, err)
		}
	}
	if got := w.Ledger.BuildsCompleted; got != len(cfg.Builds) {
		t.Fatalf("builds completed = %d, want %d", got, len(cfg.Builds))
	}

	// And no closure that would strand a known entity may start afterwards.
	w.City.RevealAll()
	depot := w.City.Depots[0].Pos
	base := w.City.Distances(depot)
	scheduled := 0
	for tick := int64(0); tick < 4000; tick++ {
		w.Tick = tick
		c, ok := w.closureAt(tick)
		if !ok {
			continue
		}
		scheduled++
		blocked := map[Position]bool{}
		for _, p := range c.Tiles {
			blocked[p] = true
		}
		after := w.City.DistancesAvoiding(depot, blocked)
		for p := range base {
			if blocked[p] {
				continue
			}
			if _, ok := after[p]; !ok {
				t.Fatalf("closure %s at tick %d cut %v off from the depot", c.ID, tick, p)
			}
		}
	}
	if scheduled == 0 {
		t.Fatal("no closures scheduled: the test proved nothing")
	}
}

// TestReloadRestoresActiveClosures is the regression test for a save taken
// during street works reloading with the street open.
//
// Closures are documented as derived-and-never-stored, which is true of the
// save format and was false of the world: `closures` is an in-memory cache, so
// ImportState produced a city with no road works while the original still had
// them, and the two worlds diverged on the next tick.
func TestReloadRestoresActiveClosures(t *testing.T) {
	w, cfg := closureWorld(t)
	w.Ledger.BuildsCompleted = 4

	// Advance to a point where a closure is live and has been for a while, so
	// the reconstruction has to find a start tick that is not the current one.
	var at int64 = -1
	for i := 0; i < 5000; i++ {
		w.Advance()
		if n := len(w.ActiveClosures()); n > 0 && w.Tick%37 == 0 {
			at = w.Tick
			break
		}
	}
	if at < 0 {
		t.Skip("no closure became live in 5000 ticks")
	}
	live := w.ActiveClosures()
	closedBefore := w.ClosedSet()
	if len(live) == 0 || len(closedBefore) == 0 {
		t.Fatalf("probe state is wrong: %d closures, %d tiles", len(live), len(closedBefore))
	}
	// The closure must already have been running for more than one tick, or the
	// test would pass even with a naive "look only at the current tick" rebuild.
	if live[0].StartTick >= w.Tick {
		t.Fatalf("closure started on the current tick; pick a later tick so the walk-back is exercised")
	}

	saved := w.ExportState()
	reloaded, err := NewWorldSeeded(w.City.Clone(), cfg, w.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ImportState(saved); err != nil {
		t.Fatal(err)
	}

	if got := reloaded.Tick; got != at {
		t.Fatalf("tick = %d, want %d", got, at)
	}
	got := reloaded.ActiveClosures()
	if len(got) != len(live) {
		t.Fatalf("active closures %d -> %d after reload", len(live), len(got))
	}
	for i := range live {
		if got[i].ID != live[i].ID {
			t.Errorf("closure %d: id %s -> %s", i, live[i].ID, got[i].ID)
		}
		if got[i].StartTick != live[i].StartTick || got[i].EndTick != live[i].EndTick {
			t.Errorf("closure %s: window [%d,%d) -> [%d,%d)", live[i].ID,
				live[i].StartTick, live[i].EndTick, got[i].StartTick, got[i].EndTick)
		}
	}
	// The tiles a car actually cannot enter must be identical.
	for p := range closedBefore {
		if !reloaded.ClosedAt(p) {
			t.Errorf("%v was closed before the reload and is open after it", p)
		}
	}
	for p := range reloaded.ClosedSet() {
		if !closedBefore[p] {
			t.Errorf("%v is closed after the reload but was open before it", p)
		}
	}

	// And the next tick must agree, which is the part that was diverging.
	w.Advance()
	reloaded.Advance()
	if len(w.ClosedSet()) != len(reloaded.ClosedSet()) {
		t.Errorf("one tick after reload the worlds disagree: %d vs %d tiles closed",
			len(w.ClosedSet()), len(reloaded.ClosedSet()))
	}
}

// TestClosedOriginIsStillDrivable pins the one asymmetry street works need: a
// closure blocks ENTRY, not occupancy, so a car standing on a tile that closes
// under it is still routable from that tile.
//
// Before this, four queries disagreed about the same tile -- stepCar let the car
// leave, RouteDistance said -1, StepToward returned a real direction, and
// estimateFrom quoted a SHORTER deadline than the open-street case because it
// skipped the negative leg. That last one is the player-visible bug: a closure
// is only ever supposed to lengthen a deadline.
func TestClosedOriginIsStillDrivable(t *testing.T) {
	w, _ := closureWorld(t)
	w.City.RevealAll()
	home := w.Cars[0].Pos
	goal := w.City.Customers[0].Pos

	openDist := w.RouteDistance(home, goal)
	openEst := w.estimateFrom(home, w.City.Kitchens[0].Pos, goal)
	if openDist < 0 {
		t.Fatalf("no route on an open map from %v to %v", home, goal)
	}

	// Close the tile the car is standing on, and nothing else.
	w.closures = []Closure{{ID: "under_car", Tiles: []Position{home}, StartTick: 0, EndTick: 100000}}
	w.Tick = 5

	// 1. Entry is still refused -- the rule is unchanged for destinations.
	if w.RouteWalkable(home) {
		t.Errorf("RouteWalkable(%v) = true for a closed tile; it is about entry and must stay false", home)
	}

	// 2. A route from a closed origin exists and matches the open-street length,
	//    because closing a tile does not move the car.
	gotDist := w.RouteDistance(home, goal)
	if gotDist < 0 {
		t.Fatalf("RouteDistance(%v,%v) = %d with the origin closed; the car can legally drive off", home, goal, gotDist)
	}
	if gotDist != openDist {
		t.Errorf("RouteDistance from a closed origin = %d, want %d (the open-street length)", gotDist, openDist)
	}

	// 3. StepToward agrees with RouteDistance rather than contradicting it.
	step := w.StepToward(home, goal)
	if gotDist >= 0 && step == "" {
		t.Errorf("RouteDistance found a route but StepToward returned \"\"")
	}

	// 4. The deadline must not shrink. This is the promise the README makes.
	gotEst := w.estimateFrom(home, w.City.Kitchens[0].Pos, goal)
	if gotEst < openEst {
		t.Errorf("deadline estimate shrank for a car on a closed tile: %d -> %d ticks", openEst, gotEst)
	}

	// 5. And the car really can leave, which is why 2-4 must hold.
	dir := w.StepToward(home, goal)
	if dir == "" {
		t.Fatalf("no escape direction from %v", home)
	}
	w.Cars[0].QueuedMove = dir
	if !w.stepCar(w.Cars[0]) {
		t.Errorf("engine refused to let a car drive off a tile that closed under it")
	}
}

// TestDistanceAvoidingStrictFormsAreUnchanged guards the other side of the
// origin rule: the guard helpers that ask "what can be reached FROM this seed"
// must keep treating a blocked start as nothing, or closureIsSafe and the build
// validator would both stop rejecting anything.
func TestDistanceAvoidingStrictFormsAreUnchanged(t *testing.T) {
	w, _ := closureWorld(t)
	w.City.RevealAll()
	start := w.City.Depots[0].Pos

	blocked := map[Position]bool{start: true}
	if got := w.City.DistancesAvoiding(start, blocked); len(got) != 0 {
		t.Errorf("DistancesAvoiding from a blocked seed returned %d tiles, want none", len(got))
	}
	if got := w.City.ReachAvoiding(start, blocked); len(got) != 0 {
		t.Errorf("ReachAvoiding from a blocked seed returned %d tiles, want none", len(got))
	}
}

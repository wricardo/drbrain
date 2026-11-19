package engine

import (
	"path/filepath"
	"testing"
)

// shippedCity loads maps/city.json, the content that actually ships.
func shippedCity(t *testing.T) (*City, *CityConfig) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	city, cfg, err := CityFromFile(path)
	if err != nil {
		t.Fatalf("load shipped city: %v", err)
	}
	return city, cfg
}

// TestEveryGatedCarClassIsGrantable is the guard against dead content.
//
// A class declaring requires_grant is advertised in the UI at its price. If no
// build in the city carries a grant_car_class effect for it, that advertisement
// is a lie: clicking it always fails. This test is what turns "multi-car is
// supported" from a claim into something the shipped map can actually deliver.
func TestEveryGatedCarClassIsGrantable(t *testing.T) {
	_, cfg := shippedCity(t)

	granted := map[string]bool{}
	for _, b := range cfg.Builds {
		for _, e := range b.Effects {
			if e.Type == EffectGrantCarClass && e.Class != "" {
				granted[e.Class] = true
			}
		}
	}

	for class, cc := range cfg.CarClasses {
		if !cc.RequiresGrant {
			continue
		}
		if !granted[class] {
			t.Errorf("car class %q requires a grant but no build grants it: "+
				"it is advertised at $%d and can never be bought", class, cc.Price)
		}
	}
}

// TestAtLeastOneCarIsBuyable keeps the multi-car promise honest: the player must
// be able to reach a second vehicle, or the fleet mechanics are unreachable.
func TestAtLeastOneCarIsBuyable(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Ledger.Money = 100000

	buyable := 0
	for _, cc := range w.CarClassCatalog() {
		if cc.Price > 0 && cc.Unlocked {
			buyable++
		}
	}
	if buyable == 0 {
		t.Fatal("no paid car class is purchasable: the player can never scale past one car")
	}
}

// TestEveryHiddenDistrictCanBeRevealed guards the other half of the "map
// evolves" promise. A district authored hidden with no reveal build is
// permanently dark: 23% of the map that no amount of play can reach.
func TestEveryHiddenDistrictCanBeRevealed(t *testing.T) {
	_, cfg := shippedCity(t)

	revealable := map[string]bool{}
	for _, b := range cfg.Builds {
		for _, e := range b.Effects {
			if e.Type == EffectRevealDistrict && e.District != "" {
				revealable[e.District] = true
			}
		}
	}

	for name, d := range cfg.Districts {
		if !d.Hidden {
			continue
		}
		if !revealable[name] {
			tiles := 0
			for _, r := range d.Rects {
				tiles += (r[2] - r[0] + 1) * (r[3] - r[1] + 1)
			}
			t.Errorf("district %q is hidden (%d tiles) but no build reveals it: "+
				"permanently unreachable", name, tiles)
		}
	}
}

// TestRevealedDistrictBecomesReachable proves the reveal actually opens the
// region: after the build, the tiles must be walkable and nothing stranded.
func TestRevealedDistrictBecomesReachable(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Ledger.Money = 100000

	// Grant every build so this test is about geography, not progression.
	for _, b := range cfg.Builds {
		w.permitted[b.ID] = true
	}

	target := Position{17, 3} // harbour charger tile
	if w.City.Walkable(target) {
		t.Fatal("precondition: harbour should start hidden")
	}

	for _, b := range cfg.Builds {
		if _, err := w.Build(b.ID); err != nil {
			t.Fatalf("build %s: %v", b.ID, err)
		}
	}

	if !w.City.Walkable(target) {
		t.Error("harbour still unreachable after building every available upgrade")
	}
	if stranded := w.City.StrandedEntities(); len(stranded) > 0 {
		t.Errorf("building everything stranded: %v", stranded)
	}
}

// TestEveryBuildIsReachableBySomeUnlock ensures no build is orphaned: a build
// that no unlock grants can never appear for the player.
func TestEveryBuildIsReachableBySomeUnlock(t *testing.T) {
	_, cfg := shippedCity(t)

	grantedBuilds := map[string]bool{}
	for _, u := range cfg.Unlocks {
		for _, id := range u.GrantsBuild {
			grantedBuilds[id] = true
		}
	}

	for _, b := range cfg.Builds {
		if !grantedBuilds[b.ID] {
			t.Errorf("build %q is granted by no unlock: unreachable content", b.ID)
		}
	}
}

// TestEveryUnlockLeadsToABuild is the inverse: an unlock that grants nothing is
// a dangling promise shown in the UI.
func TestEveryUnlockLeadsToABuild(t *testing.T) {
	_, cfg := shippedCity(t)

	exists := map[string]bool{}
	for _, b := range cfg.Builds {
		exists[b.ID] = true
	}

	for _, u := range cfg.Unlocks {
		if len(u.GrantsBuild) == 0 {
			t.Errorf("unlock %q grants no builds", u.ID)
		}
		for _, id := range u.GrantsBuild {
			if !exists[id] {
				t.Errorf("unlock %q grants unknown build %q", u.ID, id)
			}
		}
	}
}

// TestEveryCustomerHasASupplyingKitchen is a softer content check: a customer
// demanding an item no kitchen serves would only ever produce starving orders.
func TestEveryCustomerHasASupplyingKitchen(t *testing.T) {
	city, _ := shippedCity(t)
	// Hidden districts make entities unreachable by design, so evaluate the
	// fully revealed city: the question is whether supply exists at all.
	city.RevealAll()
	for _, cu := range city.Customers {
		for _, item := range cu.Demands {
			if !city.anyKitchenServes(item) {
				t.Errorf("customer %q demands %q but no kitchen serves it", cu.ID, item)
			}
		}
	}
}

// TestHiddenEntitiesAreNotStranded pins a subtle guard fix.
//
// StrandedEntities originally counted every kitchen and customer that could not
// be reached, including ones inside a hidden district. That is wrong: an entity
// in undiscovered territory is not stranded, it is simply not yet in play. The
// bug made every build fail as soon as a map authored content for a district it
// had not revealed yet, which is exactly how a map is meant to be authored.
func TestHiddenEntitiesAreNotStranded(t *testing.T) {
	city, cfg := shippedCity(t)

	hidden := 0
	for _, k := range city.Kitchens {
		if city.IsHidden(k.Pos) {
			hidden++
		}
	}
	for _, c := range city.Customers {
		if city.IsHidden(c.Pos) {
			hidden++
		}
	}
	if hidden == 0 {
		t.Skip("shipped city has no entities in hidden districts; nothing to check")
	}

	// With the districts hidden, the city must still report nothing stranded.
	if stranded := city.StrandedEntities(); len(stranded) > 0 {
		t.Fatalf("entities in hidden districts reported as stranded: %v", stranded)
	}

	bridge, ok := buildByIDFromConfig(cfg, "b02_east_bridge")
	if !ok {
		t.Fatal("shipped city has no East Bridge build")
	}
	gate, ok := buildByIDFromConfig(cfg, "b04_harbour_gate")
	if !ok {
		t.Fatal("shipped city has no Harbour Gate build")
	}
	var district string
	for _, e := range gate.Effects {
		if e.Type == EffectRevealDistrict {
			district = e.District
		}
	}
	if district == "" {
		t.Fatal("Harbour Gate reveals no district")
	}

	// The guard must still catch a genuinely cut-off region. Note that the
	// harbour build also places a charger inside the district, and a charger is
	// a reachability seed, so the harbour reaches itself. That is correct: the
	// player can always drive within a district once inside. The cut-off case
	// worth catching is a district with no seed of its own and no road in, so
	// build one to prove the guard still fires.
	cut, _, err := CityFromFile(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Drop every charger/depot seed, leaving only the depot as a seed outside
	// the harbour, then reveal the harbour unbridged: its entities must be
	// reported because nothing reaches them.
	cut.Chargers = nil
	cut.RevealRegion(cut.DistrictTiles(district))
	if stranded := cut.StrandedEntities(); len(stranded) == 0 {
		t.Fatal("a revealed district with no road in and no charger inside should strand its entities")
	}

	// And bridging first makes it safe, which is what the real build does.
	fresh2, _, err := CityFromFile(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range bridge.Effects {
		if e.Type != EffectBuildBridge {
			continue
		}
		for _, tw := range e.Tiles {
			if err := fresh2.SetTerrain(Position{tw.X, tw.Y}, RoadTile); err != nil {
				t.Fatal(err)
			}
		}
	}
	fresh2.RevealRegion(fresh2.DistrictTiles(district))
	if stranded := fresh2.StrandedEntities(); len(stranded) > 0 {
		t.Fatalf("bridged and revealed harbour should be coherent, got %v", stranded)
	}
}

// TestEveryKitchenIsReachableOnAStarterCar is the distance guard.
//
// A district can be revealed and still be useless: the harbour kitchen sat 30
// steps from the depot, beyond a scooter's 24-battery range, so a player who
// bought access still could not service it. Reachability is not enough — the
// content has to be reachable on the cheapest car, or with a charger as a
// stepping stone.
//
// This models a two-leg trip: depot -> nearest charger, then charger -> kitchen.
func TestEveryKitchenIsReachableOnAStarterCar(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Evaluate the finished city: build everything, as a progressed player has.
	for _, b := range cfg.Builds {
		w.permitted[b.ID] = true
	}
	w.Ledger.Money = 1_000_000
	for _, b := range cfg.Builds {
		if _, err := w.Build(b.ID); err != nil {
			t.Fatalf("build %s: %v", b.ID, err)
		}
	}

	var starter int
	for class, cc := range cfg.CarClasses {
		if cc.Price == 0 {
			starter = cc.MaxBattery
			_ = class
		}
	}
	if starter == 0 {
		t.Skip("no free starter car class")
	}

	for _, k := range w.City.Kitchens {
		if withinChargedRange(w, k.Pos, starter) {
			continue
		}
		t.Errorf("kitchen %q at %v is unreachable within %d battery, even via a charger",
			k.ID, k.Pos, starter)
	}
}

// withinChargedRange reports whether a car with the given battery can reach
// target directly, or by charging at some depot/charger on the way (two legs).
func withinChargedRange(w *World, target Position, battery int) bool {
	for _, d := range w.City.Depots {
		if w.City.Distance(d.Pos, target) <= battery {
			return true
		}
	}
	for _, ch := range w.City.Chargers {
		// Leg 1: depot/charger -> this charger. Leg 2: charger -> target.
		if w.City.Distance(ch.Pos, target) > battery {
			continue
		}
		for _, d := range w.City.Depots {
			if w.City.Distance(d.Pos, ch.Pos) <= battery {
				return true
			}
		}
	}
	return false
}

// TestFullProgressionIsReachableFromTheStart is the end-to-end content guard.
//
// It grants nothing: it plays the shipped city forward, spending as it goes,
// exactly as a player would, and asserts every build and unlock in the map is
// eventually earned. This is the test that would have caught both the
// unbuyable-van and permanently-dark-district bugs, which were invisible to
// unit tests because every individual rule worked.
func TestFullProgressionIsReachableFromTheStart(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 0x5eed5eed)
	if err != nil {
		t.Fatal(err)
	}

	// A patient driver: drive the single car along the shortest route to each
	// goal, and buy anything affordable. This mirrors cmd/drbrain's harness but
	// lives here so plain `go test ./...` checks it.
	for range 40_000 {
		buyWhatWeCan(w)

		// Tow stranded cars. This is not optional: a car on 0 battery is a
		// permanent dead end, so a driver that never tows stops making progress
		// forever. The first run of this test failed for exactly that reason.
		for _, c := range w.Cars {
			if c.State(w.City) == CarStranded {
				_ = w.Tow(c.ID)
			}
		}

		// Assign a pending order if a car is free.
		for _, o := range w.OpenOrders() {
			if o.State != OrderPending {
				continue
			}
			if car := freeCarFor(w, o); car != nil {
				_ = w.AssignOrder(o.ID, car.ID)
			}
		}

		// Step every car toward its goal, but let a car charge up first if it
		// is standing on a charger with a low battery. Without this a car that
		// has just been towed drives off on the one point of charge it was
		// given, strands again, and gets towed again on the next tick: an
		// infinite tow loop that looks like a content failure.
		for _, c := range w.Cars {
			if w.City.ChargingAt(c.Pos) && c.BatteryPct() < 80 {
				continue
			}
			if goal, ok := goalFor(w, c); ok {
				if dir := firstStep(w, c.Pos, goal); dir != "" {
					_ = w.QueueMove(c.ID, dir)
				}
			}
		}
		w.Advance()
	}

	unlocked := 0
	for _, u := range w.Unlocks() {
		if u.Earned {
			unlocked++
		}
	}
	if unlocked != len(w.Unlocks()) {
		var missing []string
		for _, u := range w.Unlocks() {
			if !u.Earned {
				missing = append(missing, u.ID)
			}
		}
		t.Errorf("only %d/%d unlocks earned after 40k ticks; missing: %v",
			unlocked, len(w.Unlocks()), missing)
	}

	built := 0
	for _, b := range w.Builds() {
		if b.Completed {
			built++
		}
	}
	if built != len(w.Builds()) {
		t.Errorf("only %d/%d builds completed after 40k ticks", built, len(w.Builds()))
	}

	if w.City.HiddenCount() != 0 {
		t.Errorf("%d tiles remain unexplored: the map never fully opened", w.City.HiddenCount())
	}
	if len(w.Cars) < 2 {
		t.Errorf("fleet never grew past %d car(s)", len(w.Cars))
	}
}

// buyWhatWeCan spends money the way a player would, keeping a small reserve.
func buyWhatWeCan(w *World) {
	for _, b := range w.Builds() {
		if b.Unlocked && !b.Completed && b.Applicable && w.Ledger.Money >= b.Cost+50 {
			_, _ = w.Build(b.ID)
			return
		}
	}
	if len(w.Cars) >= 8 {
		return
	}
	for _, cc := range w.CarClassCatalog() {
		if cc.Price > 0 && cc.Unlocked && w.Ledger.Money >= cc.Price+50 {
			if _, err := w.BuyCar(cc.Class, "d_main"); err == nil {
				return
			}
		}
	}
}

// freeCarFor returns a car able to take the order, or nil.
func freeCarFor(w *World, o *Order) *Car {
	for _, c := range w.Cars {
		if !c.HasSpace() || c.State(w.City) == CarStranded {
			continue
		}
		busy := false
		for _, other := range w.OpenOrders() {
			if other.AssignedCar == c.ID {
				busy = true
				break
			}
		}
		if !busy {
			return c
		}
	}
	return nil
}

// goalFor is the car's current objective.
func goalFor(w *World, c *Car) (Position, bool) {
	for _, id := range c.Cargo {
		if o, ok := w.Order(id); ok {
			for _, cu := range w.City.Customers {
				if cu.ID == o.CustomerID {
					return cu.Pos, true
				}
			}
		}
	}
	for _, o := range w.OpenOrders() {
		if o.State == OrderAssigned && o.AssignedCar == c.ID {
			for _, k := range w.City.Kitchens {
				if k.ID == o.KitchenID {
					return k.Pos, true
				}
			}
		}
	}
	if c.BatteryPct() < 60 {
		// Skip a charger another car is standing on: it holds one car, so the
		// route would be blocked and the goal unreachable.
		for _, tile := range w.City.NearestChargingTiles(c.Pos, 4) {
			if tile != c.Pos && !w.HasRoom(tile) {
				continue
			}
			return tile, true
		}
	}
	return Position{}, false
}

// firstStep returns the first direction on a shortest path.
//
// It routes around cars, not just terrain. A tile holds one car (a depot or
// charger holds its capacity), so a path through a parked car is a path the
// engine refuses — and re-planning it every tick is a permanent stall rather
// than a detour. This mirrors cmd/drbrain's harness so the content guard
// measures content rather than driver blindness.
func firstStep(w *World, from, to Position) Direction {
	return w.StepToward(from, to)
}

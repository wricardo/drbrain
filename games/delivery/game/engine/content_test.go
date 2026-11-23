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
	for _, g := range cfg.Growth {
		for _, e := range g.Effects {
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
			t.Errorf("car class %q requires a grant but no growth stage grants it: "+
				"it is advertised at $%d and can never be bought", class, cc.Price)
		}
	}
}

// TestShippedCarClassesAreMoneyOnly pins the current product decision: every
// shipped vehicle, including the truck, is purchasable with money alone. The
// grant mechanism still exists for custom maps, but no shipped class should
// depend on it.
func TestShippedCarClassesAreMoneyOnly(t *testing.T) {
	_, cfg := shippedCity(t)
	for class, cc := range cfg.CarClasses {
		if cc.RequiresGrant {
			t.Errorf("shipped car class %q is grant-gated; want money-only", class)
		}
	}
}

// TestShippedCarClassesCostMoney pins that the starting fleet is the only free
// car. A free class lets a player spawn an unlimited fleet for $0, which makes
// the money gate on fleet size meaningless and skips the whole "buy a second
// vehicle" decision.
func TestShippedCarClassesCostMoney(t *testing.T) {
	_, cfg := shippedCity(t)
	for class, cc := range cfg.CarClasses {
		if cc.Price <= 0 {
			t.Errorf("shipped car class %q costs $%d; every class beyond the initial fleet must cost money",
				class, cc.Price)
		}
	}
}

// TestShippedTruckIsBuyableFromTheStart pins the no-gate decision: the truck
// costs money and depot space, not a license. It must be purchasable before any
// growth stage has applied.
func TestShippedTruckIsBuyableFromTheStart(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Ledger.Money = 4000
	car, err := w.BuyCar("truck", "d_main")
	if err != nil {
		t.Fatalf("buy truck before any growth: %v", err)
	}
	if car.ClassName != "truck" || car.Class.Capacity != 40 {
		t.Fatalf("bought %q with capacity %d, want truck/40", car.ClassName, car.Class.Capacity)
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
// evolves" promise. A district authored hidden with no reveal stage is
// permanently dark: 23% of the map that no amount of play can reach.
func TestEveryHiddenDistrictCanBeRevealed(t *testing.T) {
	_, cfg := shippedCity(t)

	revealable := map[string]bool{}
	for _, g := range cfg.Growth {
		for _, e := range g.Effects {
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
			t.Errorf("district %q is hidden (%d tiles) but no growth stage reveals it: "+
				"permanently unreachable", name, tiles)
		}
	}
}

// TestGrowthScheduleIsOrdered keeps the timeline coherent: ids exist and are
// unique, ticks never go backwards, and the first stage is not scheduled before
// the game can plausibly reach it.
func TestGrowthScheduleIsOrdered(t *testing.T) {
	_, cfg := shippedCity(t)
	if len(cfg.Growth) == 0 {
		t.Fatal("shipped city has no growth schedule: nothing ever expands")
	}
	seen := map[string]bool{}
	var last int64
	for i, g := range cfg.Growth {
		if g.ID == "" {
			t.Errorf("growth stage %d has no id", i)
			continue
		}
		if seen[g.ID] {
			t.Errorf("duplicate growth stage id %q", g.ID)
		}
		seen[g.ID] = true
		if i > 0 && g.Tick < last {
			t.Errorf("growth stage %q at tick %d runs before the previous stage's %d", g.ID, g.Tick, last)
		}
		if g.Tick < 0 {
			t.Errorf("growth stage %q has a negative tick %d", g.ID, g.Tick)
		}
		last = g.Tick
	}
}

// TestRevealedDistrictBecomesReachable proves the reveal actually opens the
// region: after the stage applies, the tiles must be walkable and nothing
// stranded.
func TestRevealedDistrictBecomesReachable(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}

	target := Position{17, 3} // harbour charger tile
	if w.City.Walkable(target) {
		t.Fatal("precondition: harbour should start hidden")
	}

	w.Tick = cfg.Growth[len(cfg.Growth)-1].Tick
	w.applyGrowth()
	if w.growthApplied != len(cfg.Growth) {
		t.Fatalf("growth stages applied = %d, want %d", w.growthApplied, len(cfg.Growth))
	}

	if !w.City.Walkable(target) {
		t.Error("harbour still unreachable after the whole growth schedule")
	}
	if stranded := w.City.StrandedEntities(); len(stranded) > 0 {
		t.Errorf("the growth schedule stranded: %v", stranded)
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

	bridge, ok := growthByIDFromConfig(cfg, "g02_harbour_gate")
	if !ok {
		t.Fatal("shipped city has no East Bridge growth stage")
	}
	gate, ok := growthByIDFromConfig(cfg, "g02_harbour_gate")
	if !ok {
		t.Fatal("shipped city has no Harbour Gate growth stage")
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
	// Evaluate the finished city: apply the whole growth schedule, as a
	// progressed player has by the last stage.
	w.Tick = cfg.Growth[len(cfg.Growth)-1].Tick
	w.applyGrowth()

	var starter int
	for _, ic := range cfg.InitialCars {
		if cc, ok := cfg.CarClasses[ic.Class]; ok {
			starter = cc.MaxBattery
			break
		}
	}
	if starter == 0 {
		t.Skip("initial_cars names no known car class")
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

// TestGrowthScheduleCompletesInPlay is the end-to-end content guard for the
// scheduled city. It plays the shipped map forward with the same simple driver
// the upkeep tests use and asserts the whole timeline applies: every district
// opens, the chapter caps, and the map is fully visible by the last stage.
func TestGrowthScheduleCompletesInPlay(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 0x5eed5eed)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Growth) == 0 {
		t.Fatal("shipped city has no growth schedule")
	}
	last := int(cfg.Growth[len(cfg.Growth)-1].Tick)
	runSim(t, w, last+20)

	if got, want := w.growthApplied, len(cfg.Growth); got != want {
		t.Fatalf("growth stages applied = %d, want %d after %d ticks", got, want, w.Tick)
	}
	if w.City.HiddenCount() != 0 {
		t.Errorf("%d tiles remain unexplored: the schedule never opened the whole map", w.City.HiddenCount())
	}
	if got := w.Chapter(); got != cfg.MaxChapter {
		t.Errorf("chapter = %d, want %d once every stage has applied", got, cfg.MaxChapter)
	}
}

// TestGrowthStageFiresOnItsTick pins the timing: a stage applies at its tick,
// not one before and not only when the next stage comes due.
func TestGrowthStageFiresOnItsTick(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Growth) == 0 {
		t.Fatal("shipped city has no growth schedule")
	}
	first := cfg.Growth[0]

	w.Tick = first.Tick - 1
	w.applyGrowth()
	if w.growthApplied != 0 {
		t.Fatalf("stage %q applied at tick %d, before its scheduled %d", first.ID, w.Tick, first.Tick)
	}

	w.Tick = first.Tick
	w.applyGrowth()
	if w.growthApplied != 1 {
		t.Fatalf("stage %q did not apply at its scheduled tick %d", first.ID, first.Tick)
	}
}

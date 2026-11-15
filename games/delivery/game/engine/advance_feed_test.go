package engine

import (
	"strings"
	"testing"
)

// TestAdvanceReturnsItsOwnEventsWhenTheLogIsFull pins a silent data-loss bug.
//
// Advance used to return `w.Events[start:]`. Once the retained log hit
// MaxEvents, emit trimmed from the front and the slice came back empty, so a
// caller polling Advance for "what just happened" got nothing — exactly when a
// long-running session most needs the feed.
func TestAdvanceReturnsItsOwnEventsWhenTheLogIsFull(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]

	// Fill the retained log past its cap with real moves. A fixed iteration
	// count is required: the log trims back to MaxEvents, so waiting for it to
	// exceed the cap would never finish.
	dirs := []Direction{South, North}
	for i := range MaxEvents + 50 {
		_ = w.QueueMove(car.ID, dirs[i%2])
		w.Advance()
	}
	if len(w.Events) != MaxEvents {
		t.Fatalf("precondition: retained log is %d events, want it pinned at the %d cap",
			len(w.Events), MaxEvents)
	}

	evs := func() []Event {
		_ = w.QueueMove(car.ID, South)
		return w.Advance()
	}()

	if len(evs) == 0 {
		t.Fatal("Advance returned no events with a full log: the feed silently drops")
	}
	want := string(South)
	if !strings.Contains(evs[0].Message, want) {
		t.Errorf("first event = %q, want the move just applied", evs[0].Message)
	}
	if len(w.Events) > MaxEvents {
		t.Errorf("retained log is %d events, over the %d cap", len(w.Events), MaxEvents)
	}
}

// TestReputationStaysClampedWhenAchievementsGrantIt: reputation is documented as
// clamped to [0,5], and a grant is still an award. Without the clamp a single
// generous achievement pushed it to 23, which then made every reputation-gated
// unlock trivially satisfied.
func TestReputationStaysClampedWhenAchievementsGrantIt(t *testing.T) {
	city, cfg := shippedCity(t)
	cfg.Achievements = []AchievementConfig{{
		ID:    "huge",
		Title: "Huge",
		// No requirements: it fires on the first tick.
		Requires:   Requirement{},
		GrantRep:   20,
		GrantMoney: 0,
	}}
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Advance()

	if w.Ledger.Reputation > MaxReputation {
		t.Fatalf("reputation = %v after a +20 grant, want clamped to %v",
			w.Ledger.Reputation, MaxReputation)
	}
}

// TestReputationStaysClampedWhenBuildsGrantIt covers the other grant path.
func TestReputationStaysClampedWhenBuildsGrantIt(t *testing.T) {
	city, cfg := shippedCity(t)
	cfg.Builds = append(cfg.Builds, BuildConfig{
		ID:    "b_rep",
		Title: "Reputation Boost",
		Cost:  0,
		Effects: []BuildEffect{{
			Type: EffectGrantRep,
			Rep:  20,
		}},
	})
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.built = map[string]bool{}
	w.permitted = map[string]bool{"b_rep": true}

	if _, err := w.Build("b_rep"); err != nil {
		t.Fatalf("build: %v", err)
	}
	if w.Ledger.Reputation > MaxReputation {
		t.Fatalf("reputation = %v after a +20 build grant, want clamped to %v",
			w.Ledger.Reputation, MaxReputation)
	}
}

// TestOffGridMoveIsReportedAsOffGrid pins the distinction between the map edge
// and unexplored fog. Both block, but reporting a boundary as "unexplored" told
// the player to buy a district that does not exist.
func TestOffGridMoveIsReportedAsOffGrid(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]

	// Make the western border walkable and park the car on it, so the only
	// thing that can reject a westward step is the edge of the grid.
	if err := w.City.SetTerrain(Position{0, 1}, RoadTile); err != nil {
		t.Fatal(err)
	}
	car.Pos = Position{0, 1}

	if err := w.QueueMove(car.ID, West); err != nil {
		t.Fatal(err)
	}
	w.Advance()

	if car.LastReject != RejectOffGrid {
		t.Fatalf("stepping off the grid: LastReject = %q, want %q", car.LastReject, RejectOffGrid)
	}
	if car.Pos != (Position{0, 1}) {
		t.Fatalf("car moved off-grid to %s", car.Pos)
	}
}

// TestBlockedMoveIntoTerrainNamesTheTerrain keeps the message useful without
// leaking what is under the fog.
func TestBlockedMoveIntoTerrainNamesTheTerrain(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1}

	if err := w.QueueMove(car.ID, North); err != nil {
		t.Fatal(err)
	}
	evs := w.Advance()

	if car.LastReject != RejectBlocked {
		t.Fatalf("LastReject = %q, want %q", car.LastReject, RejectBlocked)
	}
	if len(evs) == 0 || !strings.Contains(evs[0].Message, string(WaterTile)) {
		t.Fatalf("blocked event should name the terrain, got %v", evs)
	}
}

// TestHiddenTileBlocksWithoutRevealingTerrain: a fogged tile must report
// "unexplored", never the terrain underneath, or the fog leaks the map.
func TestHiddenTileBlocksWithoutRevealingTerrain(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1}
	w.City.HideRect(1, 0, 1, 0) // hide the water tile north of the depot

	if err := w.QueueMove(car.ID, North); err != nil {
		t.Fatal(err)
	}
	evs := w.Advance()

	if car.LastReject != RejectBlocked {
		t.Fatalf("LastReject = %q, want %q", car.LastReject, RejectBlocked)
	}
	if len(evs) == 0 {
		t.Fatal("no event for a blocked move")
	}
	if !strings.Contains(evs[0].Message, "unexplored") {
		t.Fatalf("fogged tile should read as unexplored, got %q", evs[0].Message)
	}
	if strings.Contains(evs[0].Message, string(WaterTile)) {
		t.Fatalf("blocked message leaks hidden terrain: %q", evs[0].Message)
	}
}

// Orders for the same customer all complete on one arrival. The dropoff loop
// used to break after the first, so a loaded van had to leave and re-enter the
// address once per box — invisible with a 1-slot scooter, wrong with a van.
func TestAllCargoForOneCustomerIsDeliveredOnArrival(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	car := w.Cars[0]
	car.Class.Capacity = 12

	// A customer address that is walkable from a neighbouring tile, read from
	// the map rather than hardcoded.
	var anna Position
	for _, cu := range w.City.Customers {
		if cu.ID == "c_anna" {
			anna = cu.Pos
		}
	}
	if anna == (Position{}) {
		t.Fatal("shipped city has no c_anna")
	}
	car.Pos = anna
	car.Cargo = nil
	car.cargoBoxes = 0
	for i := range 3 {
		o := &Order{
			ID: "x" + string(rune('a'+i)), Item: "pizza", KitchenID: "k_pizza",
			CustomerID: "c_anna", State: OrderPickedUp, AssignedCar: car.ID,
			DeadlineTick: 10000, Reward: 40, Quantity: 2,
		}
		w.Orders = append(w.Orders, o)
		car.addCargo(o.ID, o.Boxes())
	}
	// resolveTileInteractions only runs for cars that moved; step in from a
	// walkable neighbour and back onto the customer tile.
	approach, ok := firstWalkableNeighbour(w.City, anna)
	if !ok {
		t.Fatal("customer tile has no walkable neighbour")
	}
	car.Pos = approach
	dir := directionBetween(approach, anna)
	if dir == "" {
		t.Fatal("no direction from approach to customer")
	}
	_ = w.QueueMove(car.ID, dir)
	w.Advance()
	done := 0
	for _, o := range w.Orders {
		if o.State == OrderDone {
			done++
		}
	}
	if done != 3 {
		t.Errorf("only %d of 3 same-customer orders delivered on arrival", done)
	}
}

// firstWalkableNeighbour returns any walkable neighbour of p.
func firstWalkableNeighbour(c *City, p Position) (Position, bool) {
	for _, d := range AllDirections {
		if n, ok := p.Neighbour(d); ok && c.Walkable(n) {
			return n, true
		}
	}
	return Position{}, false
}

// directionBetween returns the single step that takes from to to.
func directionBetween(from, to Position) Direction {
	for _, d := range AllDirections {
		if n, ok := from.Neighbour(d); ok && n == to {
			return d
		}
	}
	return ""
}

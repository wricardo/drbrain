package engine

import "testing"

// Occupancy: a tile holds one car, except a depot or charger which holds as many
// as it authors. See drbrain-delivery-9by.
//
// The rules under test, in the order they were the source of a bug:
//  1. two cars cannot land on the same tile (they used to)
//  2. a head-on pair cannot swap through each other (they used to)
//  3. a follower CAN enter a tile another car vacates this same tick, so a queue
//     does not deadlock behind its leader
//  4. a depot holds DepotConfig.Capacity cars, so parking still works
//  5. authored initial_cars may not start overlapping

// twoCars places a second car directly, bypassing BuyCar so the test is about
// movement rather than the purchase rules.
func twoCars(t *testing.T, a, b Position) (*World, *Car, *Car) {
	t.Helper()
	w := tinyWorld(t)
	first := w.Cars[0]
	first.Pos = a
	second := &Car{
		ID: "car_2", Name: "S2", ClassName: "scooter",
		Class: first.Class, Pos: b, Battery: first.Class.MaxBattery,
	}
	w.Cars = append(w.Cars, second)
	return w, first, second
}

func TestTwoCarsCannotLandOnSameTile(t *testing.T) {
	// Both drive at the same empty tile from different directions. Whichever is
	// resolved first takes it; the other must be refused, not stacked on top.
	w, a, b := twoCars(t, Position{2, 3}, Position{3, 4})
	_ = w.QueueMove(a.ID, East)  // -> (3,3)
	_ = w.QueueMove(b.ID, North) // -> (3,3)

	w.Advance()

	if a.Pos == b.Pos {
		t.Fatalf("both cars ended on %s; a tile must hold one car", a.Pos)
	}
	want := a.Pos
	if want != (Position{3, 3}) {
		t.Fatalf("lower id should take the contested tile, got %s", want)
	}
	if b.LastReject != RejectOccupied {
		t.Fatalf("losing car LastReject = %q, want %q", b.LastReject, RejectOccupied)
	}
	if b.Pos != (Position{3, 4}) {
		t.Fatalf("losing car moved to %s, want to have stayed at (3,4)", b.Pos)
	}
}

func TestHeadOnCarsCannotSwap(t *testing.T) {
	// The case the old engine allowed: A east, B west, each destination is the
	// other's tile. Neither tile ever frees up, so NEITHER move commits and the
	// pair cannot pass through each other.
	//
	// Both are refused rather than one winning. That is deliberate: a fixpoint
	// with no progress refuses every remaining move, so the answer does not
	// depend on car id. The sequential alternative — let the lower id through and
	// refuse the other — would make the rule "lower id wins" instead of "there is
	// no room", and on a one-wide street it would also teleport the winner into
	// the tile the loser still occupies in the losing car's own accounting.
	//
	// The player breaks the stand-off by reversing one car, which frees a tile.
	w, a, b := twoCars(t, Position{2, 3}, Position{3, 3})
	_ = w.QueueMove(a.ID, East)
	_ = w.QueueMove(b.ID, West)

	w.Advance()

	if a.Pos != (Position{2, 3}) || b.Pos != (Position{3, 3}) {
		t.Fatalf("a head-on pair moved: a=%s b=%s; they must not pass through each other", a.Pos, b.Pos)
	}
	if a.LastReject != RejectOccupied {
		t.Fatalf("a LastReject = %q, want %q", a.LastReject, RejectOccupied)
	}
	if b.LastReject != RejectOccupied {
		t.Fatalf("b LastReject = %q, want %q", b.LastReject, RejectOccupied)
	}

	// Reversing one car resolves it: a goes back, b then has room.
	_ = w.QueueMove(a.ID, West) // (2,3) -> (1,3), freeing nothing b needs yet
	w.Advance()
	_ = w.QueueMove(b.ID, West) // (3,3) -> (2,3), now vacated
	w.Advance()
	if b.Pos != (Position{2, 3}) {
		t.Fatalf("after backing off, b at %s want (2,3) — the stand-off must be breakable", b.Pos)
	}
}

func TestCarMayFollowIntoVacatedTile(t *testing.T) {
	// A follows B into the tile B leaves on the same tick. Without this a line of
	// cars deadlocks behind its leader on a one-wide street even though the road
	// ahead is clear.
	w, a, b := twoCars(t, Position{2, 3}, Position{3, 3})
	_ = w.QueueMove(b.ID, East) // (3,3) -> (4,3)
	_ = w.QueueMove(a.ID, East) // (2,3) -> (3,3), vacated by b

	w.Advance()

	if b.Pos != (Position{4, 3}) {
		t.Fatalf("leader at %s, want (4,3)", b.Pos)
	}
	if a.Pos != (Position{3, 3}) {
		t.Fatalf("follower at %s, want (3,3): it may enter the tile the leader vacated, reject %q",
			a.Pos, a.LastReject)
	}
}

func TestDepotHasUnlimitedParking(t *testing.T) {
	w := tinyWorld(t)
	depot := w.City.Depots[0]
	if got := w.City.CarCapacity(depot.Pos); got != UnlimitedCarCapacity {
		t.Fatalf("depot capacity = %d, want unlimited", got)
	}
	w.Ledger.Money = 10_000
	for range 10 {
		car, err := w.BuyCar("scooter", "d1")
		if err != nil {
			t.Fatalf("buy: %v", err)
		}
		if car.Pos != depot.Pos {
			t.Fatalf("bought car placed at %s, want the depot %s", car.Pos, depot.Pos)
		}
	}
	if got := w.CarsAt(depot.Pos); got != 11 {
		t.Fatalf("cars at depot = %d, want 11", got)
	}
	if w.tileFull(depot.Pos) {
		t.Fatal("depot reports full despite unlimited parking")
	}
}

func TestRoadTileHoldsExactlyOneCar(t *testing.T) {
	if got := tinyWorld(t).City.CarCapacity(Position{3, 3}); got != 1 {
		t.Fatalf("road capacity = %d, want 1", got)
	}
}

func TestInitialCarsCannotStartOverlapping(t *testing.T) {
	rows := []string{
		"WWWWW",
		"WDRRW",
		"WRRRW",
		"WRRRW",
		"WWWWW",
	}
	cfg := completeConfig(&CityConfig{
		Name: "overlap", GridSize: 5,
		Layout: rows,
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 4}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 1, MaxBattery: 10, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{
			{Class: "scooter", Pos: Position{2, 2}},
			{Class: "scooter", Pos: Position{2, 2}},
		},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("city: %v", err)
	}
	if _, err := NewWorld(city, cfg); err == nil {
		t.Fatal("expected an error for two cars authored on the same road tile")
	}
}

func TestInitialCarsMayShareADepot(t *testing.T) {
	rows := []string{
		"WWWWW",
		"WDRRW",
		"WRRRW",
		"WRRRW",
		"WWWWW",
	}
	cfg := completeConfig(&CityConfig{
		Name: "shared-depot", GridSize: 5,
		Layout: rows,
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 4}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 1, MaxBattery: 10, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{
			{Class: "scooter", Pos: Position{1, 1}},
			{Class: "scooter", Pos: Position{1, 1}},
		},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("city: %v", err)
	}
	w, err := NewWorld(city, cfg)
	if err != nil {
		t.Fatalf("two cars must be allowed to start in the same yard: %v", err)
	}
	if got := w.CarsAt(Position{1, 1}); got != 2 {
		t.Fatalf("cars at depot = %d, want 2", got)
	}
}

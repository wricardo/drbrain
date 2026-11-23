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
//  4. a depot holds any number of cars, so parking still works
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

// TestCarMayEnterOccupiedDepot: unlimited parking must apply to movement, not
// just to the capacity query and BuyCar. If resolveMoves ever stops consulting
// City.CarCapacity, a car cannot drive home to a depot that already holds the
// rest of the fleet.
func TestCarMayEnterOccupiedDepot(t *testing.T) {
	w := tinyWorld(t)
	depot := w.City.Depots[0]
	parked := w.Cars[0]
	if parked.Pos != depot.Pos {
		t.Fatalf("initial car at %s, want depot %s", parked.Pos, depot.Pos)
	}
	arriving := &Car{
		ID: "car_2", Name: "S2", ClassName: parked.ClassName,
		Class: parked.Class, Pos: Position{1, 2}, Battery: parked.Class.MaxBattery,
	}
	w.Cars = append(w.Cars, arriving)

	if err := w.QueueMove(arriving.ID, North); err != nil {
		t.Fatalf("queue: %v", err)
	}
	w.Advance()

	if arriving.Pos != depot.Pos {
		t.Fatalf("arriving car at %s, want depot %s (reject %q)",
			arriving.Pos, depot.Pos, arriving.LastReject)
	}
	if arriving.LastReject != RejectNone {
		t.Fatalf("arriving car LastReject = %q, want none", arriving.LastReject)
	}
	if got := w.CarsAt(depot.Pos); got != 2 {
		t.Fatalf("cars on depot = %d, want 2", got)
	}
}

func TestTwoCarsMayEnterDepotSameTick(t *testing.T) {
	w := tinyWorld(t)
	depot := w.City.Depots[0]
	parked := w.Cars[0]
	if parked.Pos != depot.Pos {
		t.Fatalf("initial car at %s, want depot %s", parked.Pos, depot.Pos)
	}
	south := &Car{
		ID: "car_2", Name: "S2", ClassName: parked.ClassName,
		Class: parked.Class, Pos: Position{1, 2}, Battery: parked.Class.MaxBattery,
	}
	east := &Car{
		ID: "car_3", Name: "S3", ClassName: parked.ClassName,
		Class: parked.Class, Pos: Position{2, 1}, Battery: parked.Class.MaxBattery,
	}
	w.Cars = append(w.Cars, south, east)
	if err := w.QueueMove(south.ID, North); err != nil {
		t.Fatalf("queue south car: %v", err)
	}
	if err := w.QueueMove(east.ID, West); err != nil {
		t.Fatalf("queue east car: %v", err)
	}

	w.Advance()

	for _, car := range []*Car{south, east} {
		if car.Pos != depot.Pos {
			t.Errorf("%s at %s, want depot %s (reject %q)",
				car.ID, car.Pos, depot.Pos, car.LastReject)
		}
	}
	if got := w.CarsAt(depot.Pos); got != 3 {
		t.Fatalf("cars on depot = %d, want 3", got)
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
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}}},
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
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}}},
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

// depotCrossWorld is one depot with a road on each side, so four cars can
// contend for the depot tile in a single tick. Its depot is the only unlimited
// tile; every approach is an ordinary capacity-1 road.
func depotCrossWorld(t *testing.T) *World {
	t.Helper()
	cfg := completeConfig(&CityConfig{
		Name: "depot-cross", GridSize: 5,
		Layout: []string{
			"WWWWW",
			"WRRRW",
			"WRDRW",
			"WRRRW",
			"WWWWW",
		},
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{2, 2}}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 4, MaxBattery: 10, StepCost: 1},
		},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("city: %v", err)
	}
	w, err := NewWorld(city, cfg)
	if err != nil {
		t.Fatalf("world: %v", err)
	}
	return w
}

// addDepotCar places a car directly, bypassing BuyCar so the test is about
// movement rather than the purchase rules.
func addDepotCar(t *testing.T, w *World, id string, pos Position) *Car {
	t.Helper()
	car, err := NewCar(id, id, "scooter", pos, w.carClasses)
	if err != nil {
		t.Fatalf("car %s: %v", id, err)
	}
	w.Cars = append(w.Cars, car)
	return car
}

// TestMultipleCarsEnterDepotOnSameTick is the depot's defining movement rule:
// unlike a road, every car that drives at the depot in one tick is admitted.
// With a capacity-1 destination the fixpoint would let only one through.
func TestMultipleCarsEnterDepotOnSameTick(t *testing.T) {
	w := depotCrossWorld(t)
	depot := w.City.Depots[0].Pos

	north := addDepotCar(t, w, "car_1", Position{2, 1})
	south := addDepotCar(t, w, "car_2", Position{2, 3})
	west := addDepotCar(t, w, "car_3", Position{1, 2})
	east := addDepotCar(t, w, "car_4", Position{3, 2})
	for _, q := range []struct {
		car *Car
		dir Direction
	}{
		{north, South}, {south, North}, {west, East}, {east, West},
	} {
		if err := w.QueueMove(q.car.ID, q.dir); err != nil {
			t.Fatalf("queue %s: %v", q.car.ID, err)
		}
	}

	w.Advance()

	if got := w.CarsAt(depot); got != 4 {
		t.Fatalf("cars at depot = %d, want 4: a depot admits every entrant on the tick", got)
	}
	for _, car := range []*Car{north, south, west, east} {
		if car.Pos != depot {
			t.Errorf("%s at %s, want the depot %s (reject %q)", car.ID, car.Pos, depot, car.LastReject)
		}
		if car.LastReject != RejectNone {
			t.Errorf("%s reject = %q, want none", car.ID, car.LastReject)
		}
	}
}

// TestMultipleCarsLeaveDepotOnSameTick: several cars sharing the yard can all
// drive out on the same tick to distinct tiles.
func TestMultipleCarsLeaveDepotOnSameTick(t *testing.T) {
	w := depotCrossWorld(t)
	depot := w.City.Depots[0].Pos

	north := addDepotCar(t, w, "car_1", depot)
	south := addDepotCar(t, w, "car_2", depot)
	west := addDepotCar(t, w, "car_3", depot)
	east := addDepotCar(t, w, "car_4", depot)
	cases := []struct {
		car  *Car
		dir  Direction
		want Position
	}{
		{north, North, Position{2, 1}},
		{south, South, Position{2, 3}},
		{west, West, Position{1, 2}},
		{east, East, Position{3, 2}},
	}
	for _, c := range cases {
		if err := w.QueueMove(c.car.ID, c.dir); err != nil {
			t.Fatalf("queue %s: %v", c.car.ID, err)
		}
	}

	w.Advance()

	if got := w.CarsAt(depot); got != 0 {
		t.Fatalf("cars left in the depot = %d, want 0", got)
	}
	for _, c := range cases {
		if c.car.Pos != c.want {
			t.Errorf("%s at %s, want %s (reject %q)", c.car.ID, c.car.Pos, c.want, c.car.LastReject)
		}
	}
}

// TestDepotEntryAndExitInSameTick drives one car out while two others drive in:
// a single tick must apply both directions, leaving exactly the two arrivals
// parked.
func TestDepotEntryAndExitInSameTick(t *testing.T) {
	w := depotCrossWorld(t)
	depot := w.City.Depots[0].Pos

	leaving := addDepotCar(t, w, "car_1", depot)
	fromWest := addDepotCar(t, w, "car_2", Position{1, 2})
	fromNorth := addDepotCar(t, w, "car_3", Position{2, 1})
	for _, q := range []struct {
		car *Car
		dir Direction
	}{
		{leaving, East},
		{fromWest, East},
		{fromNorth, South},
	} {
		if err := w.QueueMove(q.car.ID, q.dir); err != nil {
			t.Fatalf("queue %s: %v", q.car.ID, err)
		}
	}

	w.Advance()

	if leaving.Pos != (Position{3, 2}) {
		t.Errorf("the leaving car is at %s, want (3,2)", leaving.Pos)
	}
	if fromWest.Pos != depot || fromNorth.Pos != depot {
		t.Errorf("arrivals at %s and %s, want both at the depot %s",
			fromWest.Pos, fromNorth.Pos, depot)
	}
	if got := w.CarsAt(depot); got != 2 {
		t.Fatalf("cars at depot after the exchange = %d, want 2", got)
	}
}

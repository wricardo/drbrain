package engine

import (
	"strings"
	"testing"
)

// tinyWorld builds a 7x7 open city with one depot and one car for movement
// tests. The depot sits top-left; the rest is road so a car can roam freely.
func tinyWorld(t *testing.T) *World {
	t.Helper()

	rows := []string{
		"WWWWWWW",
		"WDDDRRR",
		"WRRRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WWWWWWW",
	}
	cfg := completeConfig(&CityConfig{
		Name:     "tiny",
		GridSize: 7,
		TickMS:   100,
		Layout:   rows,
		Legend: map[string]string{
			"W": "water", "R": "road", "D": "depot",
			"C": "charger", "G": "ground", "K": "kitchen", "H": "house",
		},
		Items:  map[string]string{"pizza": "Pizza"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 4, ChargeSlots: 4}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 1, MaxBattery: 10, StepCost: 1},
			"van":     {Capacity: 3, MaxBattery: 20, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Name: "S1", Pos: Position{1, 1}}},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("build tiny city: %v", err)
	}
	w, err := NewWorld(city, cfg)
	if err != nil {
		t.Fatalf("build tiny world: %v", err)
	}
	return w
}

func TestQueuedMoveDoesNotMoveUntilTick(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	start := car.Pos

	if err := w.QueueMove(car.ID, East); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if car.Pos != start {
		t.Fatal("queuing a move must not move the car")
	}

	w.Advance()
	if car.Pos != (Position{start.X + 1, start.Y}) {
		t.Fatalf("after tick car at %s, want (%d,%d)", car.Pos, start.X+1, start.Y)
	}
}

func TestMoveSpendsBatteryOnce(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	// Park on plain road, not the depot: standing on a charging tile would
	// refill the battery in the same tick and hide the cost.
	car.Pos = Position{2, 3}
	car.Battery = 5
	before := car.Battery

	_ = w.QueueMove(car.ID, East)
	w.Advance()

	if got, want := car.Battery, before-1; got != want {
		t.Fatalf("battery = %d, want %d", got, want)
	}
}

func TestQueuedMoveIsSingleShot(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	start := car.Pos

	_ = w.QueueMove(car.ID, East)
	w.Advance()
	afterFirst := car.Pos

	// No new command: the car must stand still on subsequent ticks.
	w.Advance()
	w.Advance()

	if car.Pos != afterFirst {
		t.Fatalf("car kept moving without a command: %s -> %s", afterFirst, car.Pos)
	}
	if afterFirst == start {
		t.Fatal("first tick did not move the car")
	}
}

func TestLatestCommandWinsWithinOneTick(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	start := car.Pos

	_ = w.QueueMove(car.ID, East)
	_ = w.QueueMove(car.ID, South) // overwrites East before the tick
	w.Advance()

	if want := (Position{start.X, start.Y + 1}); car.Pos != want {
		t.Fatalf("car at %s, want %s (last command should win)", car.Pos, want)
	}
}

func TestMoveIntoWaterIsRejectedWithoutSpendingBattery(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1}
	before := car.Battery

	_ = w.QueueMove(car.ID, North) // (1,0) is water
	w.Advance()

	if car.Pos != (Position{1, 1}) {
		t.Fatalf("car moved into water: %s", car.Pos)
	}
	if car.Battery != before {
		t.Fatalf("battery changed on a rejected move: %d -> %d", before, car.Battery)
	}
	if car.LastReject != RejectBlocked {
		t.Fatalf("LastReject = %q, want %q", car.LastReject, RejectBlocked)
	}
}

func TestMoveIntoHiddenTileIsRejected(t *testing.T) {
	w := tinyWorld(t)
	w.City.HideRect(5, 1, 5, 5) // hide the eastern column
	car := w.Cars[0]
	car.Pos = Position{4, 3}

	_ = w.QueueMove(car.ID, East)
	w.Advance()

	if car.Pos != (Position{4, 3}) {
		t.Fatalf("car entered an unexplored tile: %s", car.Pos)
	}
	if car.LastReject != RejectBlocked {
		t.Fatalf("LastReject = %q, want blocked", car.LastReject)
	}
}

func TestGroundCostsExtraBattery(t *testing.T) {
	rows := []string{
		"WWWWWWW",
		"WDGRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WWWWWWW",
	}
	cfg := completeConfig(&CityConfig{
		Name: "g", GridSize: 7,
		Layout: rows,
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot", "G": "ground"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 1}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 1, MaxBattery: 10, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Pos: Position{3, 1}}},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("city: %v", err)
	}
	w, err := NewWorld(city, cfg)
	if err != nil {
		t.Fatalf("world: %v", err)
	}
	car := w.Cars[0]
	car.Battery = 5
	before := car.Battery

	_ = w.QueueMove(car.ID, West) // onto ground at (2,1)
	w.Advance()

	if got, want := car.Battery, before-2; got != want {
		t.Fatalf("ground step cost %d, want %d", before-got, 2)
	}
}

func TestCarCannotMoveWithoutEnoughBatteryForTile(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Battery = 0
	car.Pos = Position{2, 2}
	pos := car.Pos

	_ = w.QueueMove(car.ID, East)
	w.Advance()

	if car.Pos != pos {
		t.Fatalf("car with no battery moved to %s", car.Pos)
	}
	if car.LastReject != RejectNoBattery {
		t.Fatalf("LastReject = %q, want no_battery", car.LastReject)
	}
}

func TestCarChargesOnDepotAndStopsAtMax(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1} // depot
	car.Battery = 1

	w.Advance()

	if car.Battery != 1+w.City.Charging.DepotRate {
		t.Fatalf("battery after one tick on depot = %d, want %d",
			car.Battery, 1+w.City.Charging.DepotRate)
	}

	// Charging must clamp at max, never exceed it.
	for range 20 {
		w.Advance()
	}
	if car.Battery != car.Class.MaxBattery {
		t.Fatalf("battery = %d, want max %d", car.Battery, car.Class.MaxBattery)
	}
}

func TestChargingIsNotInstant(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1}
	car.Battery = 0

	w.Advance()

	if car.Battery >= car.Class.MaxBattery {
		t.Fatal("charging should take multiple ticks, not one")
	}
	if car.State(w.City) != CarCharging {
		t.Fatalf("state = %q, want charging", car.State(w.City))
	}
}

func TestStrandedWhenBatteryHitsZeroAwayFromCharger(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{3, 3}
	car.Battery = 1

	_ = w.QueueMove(car.ID, East)
	w.Advance()

	if car.Battery != 0 {
		t.Fatalf("battery = %d, want 0", car.Battery)
	}
	if got := car.State(w.City); got != CarStranded {
		t.Fatalf("state = %q, want stranded", got)
	}
}

func TestTowReturnsCarToDepot(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{5, 5}
	car.Battery = 0

	if err := w.Tow(car.ID); err != nil {
		t.Fatalf("tow: %v", err)
	}
	if car.Pos != (Position{1, 1}) {
		t.Fatalf("towed car at %s, want depot (1,1)", car.Pos)
	}
	if car.Battery != 1 {
		t.Fatalf("towed car battery = %d, want 1 (towed, not repaired)", car.Battery)
	}
}

func TestPausedWorldDoesNotAdvance(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	pos := car.Pos
	w.Paused = true

	_ = w.QueueMove(car.ID, East)
	if evs := w.Advance(); len(evs) != 0 {
		t.Fatalf("paused advance produced %d events", len(evs))
	}
	if car.Pos != pos {
		t.Fatal("car moved while paused")
	}
	if w.Tick != 0 {
		t.Fatalf("tick = %d, want 0 while paused", w.Tick)
	}
}

func TestMultipleCarsAllMoveOnSameTick(t *testing.T) {
	w := tinyWorld(t)
	second, err := w.BuyCar("scooter", "d1")
	if err != nil {
		t.Fatalf("buy car: %v", err)
	}
	a, b := w.Cars[0], second
	a.Pos, b.Pos = Position{2, 2}, Position{3, 2}

	// Both commands queue before a single tick: this is the core multi-car
	// property. Neither should block the other.
	if err := w.QueueMove(a.ID, East); err != nil {
		t.Fatal(err)
	}
	if err := w.QueueMove(b.ID, South); err != nil {
		t.Fatal(err)
	}
	w.Advance()

	if a.Pos != (Position{3, 2}) {
		t.Fatalf("car A at %s, want (3,2)", a.Pos)
	}
	if b.Pos != (Position{3, 3}) {
		t.Fatalf("car B at %s, want (3,3)", b.Pos)
	}
}

func TestBuyCarIsLimitedByMoneyNotSpace(t *testing.T) {
	w := tinyWorld(t)
	// Give the van a price so the second purchase exercises the money gate.
	w.carClasses["van"] = CarClassConfig{Capacity: 3, MaxBattery: 20, StepCost: 1, Price: 100}
	w.Ledger.Money = 10_000
	// The depot has no fleet-size cap; buy well past its legacy capacity.
	for i := range 12 {
		if _, err := w.BuyCar("scooter", "d1"); err != nil {
			t.Fatalf("buy %d with money available: %v", i, err)
		}
	}
	if got := w.CarsAt(w.City.Depots[0].Pos); got != 13 {
		t.Fatalf("cars parked at depot = %d, want 13", got)
	}
	w.Ledger.Money = 0
	if _, err := w.BuyCar("van", "d1"); err == nil || !strings.Contains(err.Error(), "costs") {
		t.Fatalf("buy without money = %v, want a cost error", err)
	}
}

func TestBuyCarRejectsUnknownClass(t *testing.T) {
	w := tinyWorld(t)
	if _, err := w.BuyCar("hovercraft", "d1"); err == nil {
		t.Fatal("expected unknown class error")
	}
}

func TestQueueMoveRejectsUnknownCarAndDirection(t *testing.T) {
	w := tinyWorld(t)
	if err := w.QueueMove("nope", East); err == nil {
		t.Fatal("expected unknown car error")
	}
	if err := w.QueueMove(w.Cars[0].ID, Direction("northwest")); err == nil {
		t.Fatal("expected bad direction error")
	}
}

func TestTickOrderIsIndependentOfCarInsertionOrder(t *testing.T) {
	// Two cars must resolve in id order, so the same commands produce the same
	// events regardless of the order cars were added.
	//
	// The two cars move apart (A east, B south), not into each other. Before the
	// occupancy rule this test used a head-on pair, which was only ever "both
	// move" because cars could pass through each other; a head-on pair is now
	// exactly the case that must NOT resolve, and it is covered by
	// TestHeadOnCarsCannotSwap.
	w := tinyWorld(t)
	c2, err := w.BuyCar("scooter", "d1")
	if err != nil {
		t.Fatal(err)
	}
	w.Cars[0].Pos, c2.Pos = Position{2, 2}, Position{4, 2}

	_ = w.QueueMove(c2.ID, South)
	_ = w.QueueMove(w.Cars[0].ID, East)
	evs := w.Advance()

	var moved []string
	for _, e := range evs {
		if e.Type == EventMoved {
			moved = append(moved, e.CarID)
		}
	}
	if len(moved) != 2 {
		t.Fatalf("expected 2 move events, got %v", moved)
	}
	if !(moved[0] < moved[1]) {
		t.Fatalf("events not in car-id order: %v", moved)
	}
}

func TestDeterministicReplay(t *testing.T) {
	// The same command sequence applied twice must produce the same world.
	run := func() (*World, string) {
		w := tinyWorld(t)
		car := w.Cars[0]
		cmds := []Direction{South, East, East, South, West, North, East}
		for _, d := range cmds {
			_ = w.QueueMove(car.ID, d)
			w.Advance()
		}
		return w, w.City.RenderASCII(w.RenderCars())
	}
	w1, snap1 := run()
	w2, snap2 := run()

	if snap1 != snap2 {
		t.Fatalf("replays diverged:\n--1--\n%s\n--2--\n%s", snap1, snap2)
	}
	if w1.Cars[0].Battery != w2.Cars[0].Battery {
		t.Fatal("replays ended with different battery")
	}
}

func TestRenderShowsCarsAsDigits(t *testing.T) {
	w := tinyWorld(t)
	out := w.City.RenderASCII(w.RenderCars())
	if !strings.Contains(out, "1") {
		t.Fatalf("expected car glyph in render:\n%s", out)
	}
}

func TestNewWorldRejectsCarOnImpassableTile(t *testing.T) {
	cfg := completeConfig(&CityConfig{
		Name: "bad", GridSize: 5,
		Layout: []string{"WWWWW", "WDRRW", "WRRRW", "WRRRW", "WWWWW"},
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 1}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 1, MaxBattery: 5, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Pos: Position{0, 0}}},
	})
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatalf("city: %v", err)
	}
	if _, err := NewWorld(city, cfg); err == nil {
		t.Fatal("expected initial car on water to be rejected")
	}
}

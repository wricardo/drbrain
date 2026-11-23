package engine

import (
	"encoding/json"
	"errors"
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
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, ChargeSlots: 4}},
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

func TestQueuedMovesRunInOrder(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	start := car.Pos

	_ = w.QueueMove(car.ID, East)
	_ = w.QueueMove(car.ID, South) // backlog: East first, then South
	if got := car.PendingMoves(); len(got) != 2 || got[0] != East || got[1] != South {
		t.Fatalf("pending moves = %v, want [EAST SOUTH]", got)
	}

	w.Advance()
	if want := (Position{start.X + 1, start.Y}); car.Pos != want {
		t.Fatalf("first tick car at %s, want %s (first command first)", car.Pos, want)
	}
	w.Advance()
	if want := (Position{start.X + 1, start.Y + 1}); car.Pos != want {
		t.Fatalf("second tick car at %s, want %s (second command second)", car.Pos, want)
	}
	w.Advance()
	if want := (Position{start.X + 1, start.Y + 1}); car.Pos != want {
		t.Fatalf("third tick car at %s, want %s (queue drained)", car.Pos, want)
	}
}

func TestMoveQueueCap(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]

	for range MaxQueuedMoves {
		if err := w.QueueMove(car.ID, East); err != nil {
			t.Fatalf("queue step %d: %v", len(car.PendingMoves()), err)
		}
	}
	if err := w.QueueMove(car.ID, East); !errors.Is(err, ErrMoveQueueFull) {
		t.Fatalf("queue past cap = %v, want ErrMoveQueueFull", err)
	}

	// Advancing frees a slot, so the cap bounds the plan, not the session.
	w.Advance()
	if err := w.QueueMove(car.ID, East); err != nil {
		t.Fatalf("queue after one step drained: %v", err)
	}
}

func TestRejectedStepIsSpentAndNextQueuedStepRuns(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{1, 1}

	_ = w.QueueMove(car.ID, North) // (1,0) is water: rejected
	_ = w.QueueMove(car.ID, East)

	w.Advance()
	if car.LastReject != RejectBlocked {
		t.Fatalf("LastReject = %q, want %q", car.LastReject, RejectBlocked)
	}
	if car.Pos != (Position{1, 1}) {
		t.Fatalf("car moved on a rejected step: %s", car.Pos)
	}
	if got := car.PendingMoves(); len(got) != 1 || got[0] != East {
		t.Fatalf("pending moves after rejection = %v, want [EAST]", got)
	}

	w.Advance()
	if want := (Position{2, 1}); car.Pos != want {
		t.Fatalf("car at %s after the next queued step, want %s", car.Pos, want)
	}
}

func TestClearMovesDropsTheWholePlan(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	start := car.Pos

	_ = w.QueueMove(car.ID, East)
	_ = w.QueueMove(car.ID, South)
	if err := w.ClearMoves(car.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	w.Advance()
	w.Advance()

	if car.Pos != start {
		t.Fatalf("car moved after its plan was cleared: %s -> %s", start, car.Pos)
	}
	if got := car.PendingMoves(); len(got) != 0 {
		t.Fatalf("pending moves after clear = %v, want none", got)
	}
}

func TestMoveQueueSurvivesSaveRoundTrip(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	_ = w.QueueMove(car.ID, East)
	_ = w.QueueMove(car.ID, South)

	blob, err := json.Marshal(w.ExportState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var saved SavedState
	if err := json.Unmarshal(blob, &saved); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	restored := tinyWorld(t)
	if err := restored.ImportState(saved); err != nil {
		t.Fatalf("import: %v", err)
	}
	got := restored.Cars[0].PendingMoves()
	if len(got) != 2 || got[0] != East || got[1] != South {
		t.Fatalf("restored pending moves = %v, want [EAST SOUTH]", got)
	}
}

// TestImportPromotesHeadlessQueue covers a hand-edited or truncated payload:
// a queue must never be a tail with no head, or the route silently stops.
func TestImportPromotesHeadlessQueue(t *testing.T) {
	w := tinyWorld(t)
	state := w.ExportState()
	state.Cars[0].QueuedMove = ""
	state.Cars[0].MoveQueue = []Direction{East, South}

	restored := tinyWorld(t)
	if err := restored.ImportState(state); err != nil {
		t.Fatalf("import: %v", err)
	}
	got := restored.Cars[0].PendingMoves()
	if len(got) != 2 || got[0] != East || got[1] != South {
		t.Fatalf("promoted pending moves = %v, want [EAST SOUTH]", got)
	}
}

func TestTowClearsTheMoveQueue(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{4, 4}
	car.Battery = 0
	_ = w.QueueMove(car.ID, East)
	_ = w.QueueMove(car.ID, South)

	if err := w.Tow(car.ID); err != nil {
		t.Fatalf("tow: %v", err)
	}
	if got := car.PendingMoves(); len(got) != 0 {
		t.Fatalf("pending moves after tow = %v, want none: a towed car must not keep driving the old plan", got)
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
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}}},
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
	// The depot has no fleet-size cap; buy well past the old yard size.
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
	// The fleet resolves in id order, so the same commands produce the same
	// feed regardless of the order cars were added.
	//
	// Successful moves no longer emit feed entries, so the observable here is a
	// head-on pair: each destination is the other car's tile, neither move
	// commits, and both blocked events must still be reported in car-id order.
	w := tinyWorld(t)
	c2, err := w.BuyCar("scooter", "d1")
	if err != nil {
		t.Fatal(err)
	}
	w.Cars[0].Pos, c2.Pos = Position{3, 2}, Position{4, 2}

	_ = w.QueueMove(w.Cars[0].ID, East)
	_ = w.QueueMove(c2.ID, West)
	evs := w.Advance()

	var blocked []string
	for _, e := range evs {
		if e.Type == EventBlocked {
			blocked = append(blocked, e.CarID)
		}
	}
	if len(blocked) != 2 {
		t.Fatalf("expected 2 blocked events, got %v", blocked)
	}
	if !(blocked[0] < blocked[1]) {
		t.Fatalf("events not in car-id order: %v", blocked)
	}
}

func TestMovementDoesNotEnterTheFeed(t *testing.T) {
	// Driving is the most frequent thing a session does, and one entry per car
	// per tick filled the capped ring with noise. A move is visible on the map,
	// so it must not be stored or returned as an event.
	w := tinyWorld(t)
	car := w.Cars[0]
	car.Pos = Position{2, 2}

	_ = w.QueueMove(car.ID, East)
	evs := w.Advance()

	if car.Pos != (Position{3, 2}) {
		t.Fatalf("car at %s after moving east, want (3,2)", car.Pos)
	}
	if len(evs) != 0 {
		t.Fatalf("a move returned %d events, want none", len(evs))
	}
	if len(w.Events) != 0 {
		t.Fatalf("a move stored %d events, want none", len(w.Events))
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
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}}},
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

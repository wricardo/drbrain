package engine

import "testing"

// fleetWorld builds a tiny world with upkeep switched on.
//
// Interval 10 and per-box 4 means a capacity-1 scooter pays 1 per interval and
// a capacity-3 van pays 3, so a single test can tell the two apart.
func fleetWorld(t *testing.T, interval int) *World {
	t.Helper()
	w := tinyWorldT(t)
	w.fleet = FleetConfig{
		UpkeepIntervalTicks:      interval,
		UpkeepBoxDivisor:         4,
		UpkeepPerBoxPerInterval:  4,
		DefaultUpkeepPerInterval: 1,
		TowFeePerTile:            2,
		TowFeeMin:                25,
	}
	return w
}

// tinyWorldT is tinyWorld with the config handed back, so a test can mutate the
// pacing tables the world copied at construction.
func tinyWorldT(t *testing.T) *World {
	t.Helper()
	return tinyWorld(t)
}

// TestUpkeepIsOffByDefault is the compatibility guarantee: a map that omits the
// `fleet` block must behave exactly as it did before upkeep existed, or every
// existing save is silently repriced on upgrade.
func TestUpkeepIsOffByDefault(t *testing.T) {
	w := tinyWorld(t)
	if w.upkeepEnabled() {
		t.Fatal("upkeep enabled on a world with no fleet config")
	}
	w.Ledger.Money = 100
	for range 100 {
		w.AdvanceNoPause()
	}
	if w.Ledger.Money != 100 || w.Ledger.UpkeepPaid != 0 {
		t.Fatalf("money %d upkeepPaid %d, want 100 and 0 with upkeep off",
			w.Ledger.Money, w.Ledger.UpkeepPaid)
	}
}

// TestParkedCarCostsNothingAndUnparkedCarCosts proves the single rule the
// mechanic is made of, by moving the same car one tile off the depot.
func TestParkedCarCostsNothingAndUnparkedCarCosts(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0] // starts at the depot (1,1)
	w.Ledger.Money = 1000

	if got := w.UpkeepCost(car); got != 0 {
		t.Fatalf("parked car upkeep = %d, want 0", got)
	}

	for range 10 {
		w.AdvanceNoPause()
	}
	if w.Ledger.UpkeepPaid != 0 {
		t.Fatalf("upkeepPaid = %d after 10 parked ticks, want 0", w.Ledger.UpkeepPaid)
	}

	// One step east is road, not depot.
	car.Pos = Position{4, 1}
	if got := w.UpkeepCost(car); got != 1 {
		t.Fatalf("unparked scooter upkeep = %d, want 1", got)
	}
	for range 10 {
		w.AdvanceNoPause()
	}
	if w.Ledger.UpkeepPaid != 1 {
		t.Fatalf("upkeepPaid = %d after 10 unparked ticks, want 1 (one interval)", w.Ledger.UpkeepPaid)
	}
	if w.Ledger.Money != 999 {
		t.Fatalf("money = %d, want 999", w.Ledger.Money)
	}
}

// TestUpkeepRateScalesWithCapacity is the "different cost per vehicle" contract:
// the van in this map has 3x the scooter's hold and must cost 3x to keep.
func TestUpkeepRateScalesWithCapacity(t *testing.T) {
	w := fleetWorld(t, 10)
	scooter := w.Cars[0]
	scooter.Pos = Position{4, 1} // off the depot, or both cars are free
	van, err := NewCar("van_1", "V", "van", Position{4, 1}, map[string]CarClassConfig{
		"van": {Capacity: 3, MaxBattery: 20, StepCost: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Cars = append(w.Cars, van)

	if s, v := w.UpkeepCost(scooter), w.UpkeepCost(van); s != 1 || v != 3 {
		t.Fatalf("scooter %d van %d, want 1 and 3", s, v)
	}
	if got := w.FleetUpkeep(); got != 4 {
		t.Fatalf("fleet upkeep = %d, want 4", got)
	}
}

// TestExplicitClassUpkeepOverridesCapacity proves the escape hatch works, so a
// map can price a class against something other than its hold size.
func TestExplicitClassUpkeepOverridesCapacity(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0]
	car.Pos = Position{4, 1} // off the depot, or the cost is legitimately 0
	car.Class.UpkeepPerInterval = 7
	if got := w.UpkeepCost(car); got != 7 {
		t.Fatalf("upkeep = %d, want the explicit 7", got)
	}
}

// TestUpkeepNeverDrivesMoneyNegative pins the clamp. expireOrders already
// behaves this way, and a balance that can go negative would make the build
// affordability checks lie.
func TestUpkeepNeverDrivesMoneyNegative(t *testing.T) {
	w := fleetWorld(t, 10)
	w.Cars[0].Pos = Position{4, 1}
	w.Ledger.Money = 0
	for range 30 {
		w.AdvanceNoPause()
	}
	if w.Ledger.Money < 0 {
		t.Fatalf("money = %d, want >= 0", w.Ledger.Money)
	}
	if w.Ledger.UpkeepPaid != 0 {
		t.Fatalf("upkeepPaid = %d with an empty balance, want 0", w.Ledger.UpkeepPaid)
	}
}

// TestUpkeepEmitsOneEventNotOnePerCar guards the event ring. The log is capped
// at max_events=500, so a broke fleet billing per car per interval would flush
// the session's whole history.
func TestUpkeepEmitsOneEventNotOnePerCar(t *testing.T) {
	w := fleetWorld(t, 10)
	w.Ledger.Money = 0
	for i := range 5 {
		van, err := NewCar("v"+string(rune('0'+i)), "V", "van", Position{3, 3},
			map[string]CarClassConfig{"van": {Capacity: 3, MaxBattery: 20, StepCost: 1}})
		if err != nil {
			t.Fatal(err)
		}
		w.Cars = append(w.Cars, van)
	}
	for range 10 {
		w.AdvanceNoPause()
	}
	n := 0
	for _, e := range w.Events {
		if e.Type == EventUpkeep {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("upkeep events = %d after one interval, want 1 regardless of fleet size", n)
	}
}

// TestTowChargesByDistance is the fee that stops Tow being a free teleport,
// which is what would otherwise make every upkeep rule optional.
func TestTowChargesByDistance(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0]
	car.Pos = Position{5, 5}
	w.Ledger.Money = 1000

	fee := w.TowFee(car, Position{1, 1})
	if fee < 25 {
		t.Fatalf("tow fee = %d, want at least the 25 floor", fee)
	}
	if err := w.Tow(car.ID); err != nil {
		t.Fatalf("tow: %v", err)
	}
	if w.Ledger.Money != 1000-fee {
		t.Fatalf("money = %d, want %d", w.Ledger.Money, 1000-fee)
	}
	if w.Ledger.TowFees != fee {
		t.Fatalf("towFees = %d, want %d", w.Ledger.TowFees, fee)
	}
}

// TestTowWhenBrokeStillWorksClamped is the anti-deadlock rule. A player with a
// stranded car and no money must still be able to recover it: refusing the tow
// would leave abandoning the save as the only exit.
func TestTowWhenBrokeStillWorksClamped(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0]
	car.Pos = Position{5, 5}
	car.Battery = 0
	w.Ledger.Money = 0

	if err := w.Tow(car.ID); err != nil {
		t.Fatalf("broke tow must still succeed, got %v", err)
	}
	if car.Pos != (Position{1, 1}) {
		t.Fatalf("car at %s, want depot", car.Pos)
	}
	if w.Ledger.Money != 0 {
		t.Fatalf("money = %d, want 0 (clamped, never negative)", w.Ledger.Money)
	}
}

// TestUpkeepSurvivesSaveRoundTrip proves the new ledger counters are persisted.
// The save format's documented failure mode is a field the restore path forgets,
// which reads as zero after a reload.
func TestUpkeepSurvivesSaveRoundTrip(t *testing.T) {
	w := fleetWorld(t, 10)
	w.Cars[0].Pos = Position{4, 1}
	w.Ledger.Money = 1000
	for range 10 {
		w.AdvanceNoPause()
	}

	paid, fees := w.Ledger.UpkeepPaid, w.Ledger.TowFees
	if paid == 0 {
		t.Fatal("expected some upkeep paid before saving")
	}
	saved := w.ExportState()
	if saved.Ledger.UpkeepPaid != paid || saved.Ledger.TowFees != fees {
		t.Fatalf("exported upkeep %d/%d, want %d/%d",
			saved.Ledger.UpkeepPaid, saved.Ledger.TowFees, paid, fees)
	}
}

// TestUpkeepIntervalRejectsNegative stops a hand-edited map from turning the
// mechanic into a per-tick charge by accident.
func TestUpkeepIntervalRejectsNegative(t *testing.T) {
	cfg := completeConfig(&CityConfig{Name: "x", GridSize: 3, Layout: []string{"DDD", "DDD", "DDD"},
		Legend: map[string]string{"D": "depot"}})
	cfg.Fleet.UpkeepIntervalTicks = -5
	if err := cfg.validate(); err == nil {
		t.Fatal("negative upkeep_interval_ticks was accepted")
	}
}

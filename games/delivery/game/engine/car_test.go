package engine

import (
	"strings"
	"testing"
)

// TestCarStatePriority pins the reported status: a flat car on a charger is
// recovering, not stranded, and cargo outranks an idle car on a charger.
func TestCarStatePriority(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	depot := w.City.Depots[0].Pos

	car.Battery = 0
	car.Pos = depot
	if got := car.State(w.City); got != CarCharging {
		t.Errorf("flat car on a charger = %q, want %q", got, CarCharging)
	}

	car.Pos = Position{2, 2}
	if got := car.State(w.City); got != CarStranded {
		t.Errorf("flat car off a charger = %q, want %q", got, CarStranded)
	}

	car.Battery = car.Class.MaxBattery
	car.Cargo = []string{"o1"}
	if got := car.State(w.City); got != CarCarrying {
		t.Errorf("loaded car = %q, want %q", got, CarCarrying)
	}

	car.Cargo = nil
	car.Pos = depot
	car.Battery = car.Class.MaxBattery / 2
	if got := car.State(w.City); got != CarCharging {
		t.Errorf("partially charged car on a charger = %q, want %q", got, CarCharging)
	}

	car.Battery = car.Class.MaxBattery
	if got := car.State(w.City); got != CarIdle {
		t.Errorf("full car on a charger = %q, want %q", got, CarIdle)
	}
}

// TestCarHoldCoercesMalformedQuantities pins the coercion and the floor at zero:
// a zero/negative box count counts as one box, and removing more boxes than the
// car holds cannot drive the load negative.
func TestCarHoldCoercesMalformedQuantities(t *testing.T) {
	car, err := NewCar("c1", "S1", "scooter", Position{1, 1}, map[string]CarClassConfig{
		"scooter": {Capacity: 4, MaxBattery: 10, StepCost: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !car.HasSpace() || !car.SpaceFor(0) || !car.SpaceFor(-5) {
		t.Fatal("an empty car must accept a malformed (<=0) quantity as one box")
	}
	if car.SpaceFor(5) {
		t.Fatal("a 4-box car must reject a 5-box order")
	}

	car.addCargo("o1", 0)
	if car.Load() != 1 {
		t.Fatalf("load after a zero-box add = %d, want 1", car.Load())
	}
	if car.FreeBoxes() != 3 {
		t.Fatalf("free boxes = %d, want 3", car.FreeBoxes())
	}

	car.addCargo("o2", 9)
	if car.FreeBoxes() != 0 || car.HasSpace() || car.SpaceFor(1) {
		t.Fatalf("overloaded car: free=%d hasSpace=%v", car.FreeBoxes(), car.HasSpace())
	}

	car.removeCargo("o1", 0)
	if car.Load() != 9 {
		t.Fatalf("load after removing a zero-box order = %d, want 9", car.Load())
	}
	car.removeCargo("o2", 99)
	if car.Load() != 0 {
		t.Fatalf("load after an oversized removal = %d, want 0", car.Load())
	}
	// Removing an id the car does not hold must still not corrupt the count.
	car.addCargo("o3", 2)
	car.removeCargo("missing", 1)
	if car.Load() != 1 {
		t.Fatalf("load after removing an unknown id = %d, want 1", car.Load())
	}
}

// TestBatteryPctAndString: the percentage truncates, a class without a battery
// reports 0 instead of dividing by zero, and the status line carries the hold
// and charge the CLI shows.
func TestBatteryPctAndString(t *testing.T) {
	car := &Car{
		ID: "c1", Name: "Van at depot", Pos: Position{2, 3},
		Class: CarClassConfig{Capacity: 4, MaxBattery: 24}, Battery: 12, cargoBoxes: 1,
	}
	if got := car.BatteryPct(); got != 50 {
		t.Fatalf("battery pct = %d, want 50", got)
	}

	car.Class.MaxBattery = 0
	if got := car.BatteryPct(); got != 0 {
		t.Fatalf("battery pct without a battery = %d, want 0", got)
	}

	car.Class.MaxBattery = 24
	want := "c1 Van at depot at (2,3) battery 12/24 cargo 1/4 boxes"
	if got := car.String(); got != want {
		t.Fatalf("string = %q, want %q", got, want)
	}
}

// TestNewCarRejectsUnusableClasses: construction must fail before a car with a
// zero hold or a zero battery can join the fleet.
func TestNewCarRejectsUnusableClasses(t *testing.T) {
	classes := map[string]CarClassConfig{
		"good":    {Capacity: 4, MaxBattery: 10, StepCost: 0},
		"no-batt": {Capacity: 4, MaxBattery: 0},
		"no-hold": {Capacity: 0, MaxBattery: 10},
	}
	cases := []struct{ class, want string }{
		{"nope", `unknown car class "nope"`},
		{"no-batt", `non-positive max_battery`},
		{"no-hold", `non-positive capacity`},
	}
	for _, tc := range cases {
		if _, err := NewCar("c1", "x", tc.class, Position{0, 0}, classes); err == nil {
			t.Errorf("%s: expected an error", tc.class)
		} else if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.class, err, tc.want)
		}
	}

	car, err := NewCar("c1", "S1", "good", Position{2, 2}, classes)
	if err != nil {
		t.Fatal(err)
	}
	if car.Battery != 10 {
		t.Errorf("a new car must start full, battery = %d", car.Battery)
	}
	if car.Class.StepCost != 1 {
		t.Errorf("step cost = %d, want the 1 default", car.Class.StepCost)
	}
}

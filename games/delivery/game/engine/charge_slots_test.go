package engine

import (
	"strings"
	"testing"
)

// chargeTestWorld builds a city whose only feature is one depot holding two
// scooters, so charging can be observed without orders or movement in play.
// slots is the charging limit.
func chargeTestWorld(t *testing.T, slots int) *World {
	t.Helper()

	cfg := completeConfig(&CityConfig{
		Name:     "charging",
		GridSize: 5,
		TickMS:   100,
		Layout: []string{
			"WWWWW",
			"WDRRW",
			"WRRRW",
			"WRRRW",
			"WWWWW",
		},
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		Items:  map[string]string{},
		Depots: []DepotConfig{{
			ID: "d1", Pos: Position{1, 1}, ChargeSlots: slots,
		}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 4, MaxBattery: 10, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{
			{Class: "scooter", Name: "S1", Pos: Position{1, 1}},
			{Class: "scooter", Name: "S2", Pos: Position{1, 1}},
		},
		Charging: ChargingConfig{DepotRate: 5, ChargerRate: 2},
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

// TestChargeSlotsLimitSimultaneousCharging pins the depot contract: with fewer
// charging bays than cars, one car charges per tick, chosen in id order, and the
// other is not starved once the first is full.
func TestChargeSlotsLimitSimultaneousCharging(t *testing.T) {
	w := chargeTestWorld(t, 1)
	for _, c := range w.Cars {
		c.Battery = 1
	}

	w.Advance()

	first, _ := w.Car("car_1")
	second, _ := w.Car("car_2")
	if first.Battery != 6 {
		t.Errorf("car_1 battery after one tick = %d, want 6 (first slot)", first.Battery)
	}
	if second.Battery != 1 {
		t.Errorf("car_2 battery while the bay is taken = %d, want 1 (unchanged)", second.Battery)
	}

	for range 5 {
		w.Advance()
	}
	if !first.Full() || !second.Full() {
		t.Errorf("cars did not both recharge: car_1 %d/%d, car_2 %d/%d",
			first.Battery, first.Class.MaxBattery, second.Battery, second.Class.MaxBattery)
	}
}

// TestChargeSlotsZeroIsUnlimited keeps maps authored before charge_slots
// existed charging every car at once, rather than silently losing capacity.
func TestChargeSlotsZeroIsUnlimited(t *testing.T) {
	w := chargeTestWorld(t, 0)
	for _, c := range w.Cars {
		c.Battery = 1
	}

	w.Advance()

	for _, c := range w.Cars {
		if c.Battery != 6 {
			t.Errorf("%s battery = %d, want 6 (no slot limit)", c.Name, c.Battery)
		}
	}
}

// TestDuplicateDepotIDRejected covers the last-corner bug: two depots with the
// same id resolved to whichever was found first, so tows and upgrades targeted
// a depot the player did not mean. Duplicates must fail the load.
func TestDuplicateDepotIDRejected(t *testing.T) {
	base := func() *CityConfig {
		return &CityConfig{
			Name:     "dup",
			GridSize: 5,
			Layout: []string{
				"WWWWW",
				"WDRRW",
				"WRRRW",
				"WDRDW",
				"WWWWW",
			},
			Legend: map[string]string{"W": "water", "R": "road", "D": "depot"},
		}
	}

	// The duplicate is deliberately non-adjacent: comparing only neighbouring
	// entries (the old check) misses it.
	dup := base()
	dup.Depots = []DepotConfig{
		{ID: "d_main", Pos: Position{1, 1}},
		{ID: "d_other", Pos: Position{1, 3}},
		{ID: "d_main", Pos: Position{3, 3}},
	}
	if _, err := NewCity(dup); err == nil || !strings.Contains(err.Error(), "duplicate depot id") {
		t.Errorf("duplicate depot id error = %v, want one mentioning duplicate depot id", err)
	}

	empty := base()
	empty.Depots = []DepotConfig{{ID: "", Pos: Position{1, 1}}}
	if _, err := NewCity(empty); err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Errorf("empty depot id error = %v, want one mentioning empty id", err)
	}
}

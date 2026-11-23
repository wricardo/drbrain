package engine

import (
	"encoding/json"
	"testing"
)

// paidChargerWorld builds a 5x5 city with a depot at (1,1) and one standalone
// charger at (2,2), with the car starting empty on the charger. price is the
// per-charger override; the city default is $1/tick.
func paidChargerWorld(t *testing.T, rate, price, maxBattery int) *World {
	t.Helper()
	priceOverride := price
	cfg := completeConfig(&CityConfig{
		Name:     "paid charging",
		GridSize: 5,
		TickMS:   100,
		Layout: []string{
			"WWWWW",
			"WDRRW",
			"WRCRW",
			"WRRRW",
			"WWWWW",
		},
		Legend: map[string]string{"W": "water", "R": "road", "D": "depot", "C": "charger"},
		Items:  map[string]string{},
		Depots: []DepotConfig{{
			ID: "d_main", Pos: Position{1, 1}, ChargeSlots: 2,
		}},
		Chargers: []ChargerConfig{{
			ID: "ch_paid", Pos: Position{2, 2}, Capacity: 1,
			Rate: rate, PricePerTick: &priceOverride,
		}},
		Charging: ChargingConfig{
			DepotRate: 5, ChargerRate: 2, ChargerPricePerTick: 1,
		},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: 4, MaxBattery: maxBattery, StepCost: 1},
		},
		InitialCars: []InitialCarConfig{
			{Class: "scooter", Name: "S1", Pos: Position{2, 2}},
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
	w.Ledger.Money = 100
	w.Ledger.Cash = nil
	w.Ledger.NextCashSeq = 1
	w.Cars[0].Battery = 0
	return w
}

// TestDepotChargingIsFree pins the one exemption: a city default charger price
// must never be charged at the depot, which is the free home base.
func TestDepotChargingIsFree(t *testing.T) {
	w := tinyWorld(t)
	w.City.Charging.ChargerPricePerTick = 9
	car := w.Cars[0]
	car.Pos = Position{1, 1}
	car.Battery = 1

	w.Advance()

	if want := 1 + w.City.Charging.DepotRate; car.Battery != want {
		t.Fatalf("battery = %d, want %d", car.Battery, want)
	}
	if len(w.Ledger.Cash) != 0 || w.Ledger.Money != 0 {
		t.Fatalf("depot charging moved money: cash=%+v money=%d", w.Ledger.Cash, w.Ledger.Money)
	}
	if car.ChargeDue != 0 {
		t.Fatalf("depot left an open charge bill of $%d", car.ChargeDue)
	}
}

// TestPaidChargerBillsOneSession checks the whole shape of a charge: energy is
// delivered per tick, the bill is one statement line when the battery fills,
// and the line carries the car and charger.
func TestPaidChargerBillsOneSession(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 6)
	car := w.Cars[0]

	w.Advance()
	w.Advance()
	if len(w.Ledger.Cash) != 0 {
		t.Fatalf("session billed before it ended: %+v", w.Ledger.Cash)
	}
	if car.ChargeDue != 2 || car.ChargeUnits != 4 {
		t.Fatalf("open session = $%d / %d units, want $2 / 4", car.ChargeDue, car.ChargeUnits)
	}

	w.Advance() // battery 6/6, session settles
	if car.Battery != 6 || car.ChargeDue != 0 || car.ChargeUnits != 0 {
		t.Fatalf("after full charge: battery=%d due=$%d units=%d", car.Battery, car.ChargeDue, car.ChargeUnits)
	}
	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %+v, want one charging line", w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashCharging || e.Amount != -3 || e.Balance != 97 {
		t.Fatalf("entry = %+v, want CHARGING -$3 balance $97", e)
	}
	if e.CarID != car.ID || e.CarName != car.Name {
		t.Errorf("car tag = %q/%q, want %q/%q", e.CarID, e.CarName, car.ID, car.Name)
	}
	if e.Memo != "6 units at ch_paid" {
		t.Errorf("memo = %q", e.Memo)
	}
	checkCashChain(t, w)

	// A full car takes no bay and must not keep billing.
	w.Advance()
	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("full car kept billing: %+v", w.Ledger.Cash)
	}
}

// TestChargerProratesPartialTick proves a top-up pays for the energy it got,
// not for a whole tick it did not need.
func TestChargerProratesPartialTick(t *testing.T) {
	w := paidChargerWorld(t, 6, 4, 24)
	car := w.Cars[0]
	car.Battery = 23

	w.Advance()

	if car.Battery != 24 || car.ChargeDue != 0 {
		t.Fatalf("battery=%d due=$%d, want full and settled", car.Battery, car.ChargeDue)
	}
	if len(w.Ledger.Cash) != 1 || w.Ledger.Cash[0].Amount != -1 {
		t.Fatalf("cash = %+v, want a single -$1 partial tick", w.Ledger.Cash)
	}
	if w.Ledger.Cash[0].Memo != "1 unit at ch_paid" {
		t.Errorf("memo = %q", w.Ledger.Cash[0].Memo)
	}
}

// TestSuperchargerIsFasterAndCostsMore is the configuration contract for a
// supercharger: a higher rate and a higher per-tick price, no special casing.
func TestSuperchargerIsFasterAndCostsMore(t *testing.T) {
	normal := paidChargerWorld(t, 2, 1, 12)
	super := paidChargerWorld(t, 6, 4, 12)

	normal.Advance()
	super.Advance()
	if n, s := normal.Cars[0].Battery, super.Cars[0].Battery; n != 2 || s != 6 {
		t.Fatalf("battery after one tick: normal %d, super %d; want 2 and 6", n, s)
	}

	for range 5 {
		normal.Advance()
	}
	super.Advance()
	if normal.Cars[0].Battery != 12 || super.Cars[0].Battery != 12 {
		t.Fatalf("both should be full: normal %d, super %d", normal.Cars[0].Battery, super.Cars[0].Battery)
	}
	normalCost := -normal.Ledger.Cash[0].Amount
	superCost := -super.Ledger.Cash[0].Amount
	if normalCost != 6 || superCost != 8 {
		t.Fatalf("full-charge cost: normal $%d, super $%d; want $6 and $8", normalCost, superCost)
	}
	if superCost <= normalCost {
		t.Fatalf("supercharger cost $%d must exceed normal $%d", superCost, normalCost)
	}
}

// TestLeavingChargerSettlesSession covers the other session boundary: a car
// that drives away mid-charge still pays for what it took.
func TestLeavingChargerSettlesSession(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 6)
	car := w.Cars[0]
	w.Advance()
	w.Advance()
	if car.Battery != 4 || car.ChargeDue != 2 {
		t.Fatalf("setup: battery=%d due=$%d", car.Battery, car.ChargeDue)
	}

	if err := w.QueueMove(car.ID, South); err != nil {
		t.Fatal(err)
	}
	w.Advance()

	if car.Pos != (Position{2, 3}) {
		t.Fatalf("car did not leave charger: %s", car.Pos)
	}
	if car.ChargeDue != 0 || len(w.Ledger.Cash) != 1 {
		t.Fatalf("leaving did not settle: due=$%d cash=%+v", car.ChargeDue, w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashCharging || e.Amount != -2 || e.Memo != "4 units at ch_paid" {
		t.Fatalf("entry = %+v", e)
	}
}

// TestTowSettlesChargingBeforeTowFee keeps the statement honest when a car is
// recalled off a paid bay: energy already received is billed first.
func TestTowSettlesChargingBeforeTowFee(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 12)
	w.fleet = FleetConfig{TowFeePerTile: 2, TowFeeMin: 25}
	w.Ledger.Money = 100
	car := w.Cars[0]
	w.Advance()
	w.Advance()
	if car.ChargeDue != 2 {
		t.Fatalf("setup due = $%d, want $2", car.ChargeDue)
	}

	if err := w.Tow(car.ID); err != nil {
		t.Fatal(err)
	}

	if len(w.Ledger.Cash) != 2 {
		t.Fatalf("cash = %+v, want charging then tow", w.Ledger.Cash)
	}
	if w.Ledger.Cash[0].Kind != CashCharging || w.Ledger.Cash[0].Amount != -2 {
		t.Errorf("first entry = %+v, want charging -$2", w.Ledger.Cash[0])
	}
	if w.Ledger.Cash[1].Kind != CashTow || w.Ledger.Cash[1].Amount != -25 {
		t.Errorf("second entry = %+v, want tow -$25", w.Ledger.Cash[1])
	}
	if car.ChargeDue != 0 {
		t.Errorf("car still owes $%d after tow", car.ChargeDue)
	}
	checkCashChain(t, w)
}

// TestChargerInheritsCityDefaults covers the normal map case: a charger that
// omits rate/price uses the city's charger rate and $/tick, and an explicit
// zero price is a free charger rather than "inherit".
func TestChargerInheritsCityDefaults(t *testing.T) {
	base := func(price *int) *CityConfig {
		return completeConfig(&CityConfig{
			Name: "defaults", GridSize: 5, TickMS: 100,
			Layout: []string{
				"WWWWW",
				"WDRRW",
				"WRCRW",
				"WRRRW",
				"WWWWW",
			},
			Legend: map[string]string{"W": "water", "R": "road", "D": "depot", "C": "charger"},
			Items:  map[string]string{},
			Depots: []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
			Chargers: []ChargerConfig{{
				ID: "ch", Pos: Position{2, 2}, Capacity: 1, PricePerTick: price,
			}},
			Charging: ChargingConfig{DepotRate: 5, ChargerRate: 2, ChargerPricePerTick: 1},
			CarClasses: map[string]CarClassConfig{
				"scooter": {Capacity: 1, MaxBattery: 6, StepCost: 1},
			},
			InitialCars: []InitialCarConfig{{Class: "scooter", Pos: Position{1, 1}}},
		})
	}

	city, err := NewCity(base(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := city.Chargers[0]; got.Rate != 2 || got.PricePerTick != 1 {
		t.Fatalf("inherited charger = %+v, want rate 2 price 1", got)
	}

	free := 0
	city, err = NewCity(base(&free))
	if err != nil {
		t.Fatal(err)
	}
	if got := city.Chargers[0].PricePerTick; got != 0 {
		t.Fatalf("explicit zero price inherited $%d; want free", got)
	}
}

// TestNegativeChargingPricesRejected catches a typo that would otherwise
// silently become a free charger or a negative bill.
func TestNegativeChargingPricesRejected(t *testing.T) {
	badGlobal := &CityConfig{
		GridSize: 5,
		Layout:   []string{"WWWWW", "WDRRW", "WRRRW", "WRRRW", "WWWWW"},
		Legend:   map[string]string{"W": "water", "R": "road", "D": "depot"},
		Items:    map[string]string{},
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
		Charging: ChargingConfig{ChargerPricePerTick: -1},
	}
	if _, err := NewCity(badGlobal); err == nil {
		t.Fatal("negative charging.charger_price_per_tick accepted")
	}

	negative := -1
	badCharger := &CityConfig{
		GridSize: 5,
		Layout:   []string{"WWWWW", "WDRRW", "WRCRW", "WRRRW", "WWWWW"},
		Legend:   map[string]string{"W": "water", "R": "road", "D": "depot", "C": "charger"},
		Items:    map[string]string{},
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
		Chargers: []ChargerConfig{{ID: "ch", Pos: Position{2, 2}, PricePerTick: &negative}},
	}
	if _, err := NewCity(badCharger); err == nil {
		t.Fatal("negative charger price_per_tick accepted")
	}
}

// TestBuiltChargerInheritsCityChargingDefaults pins the build path: a
// build-added charger must get the same rate and price as an authored one, or
// a late-game charger would quietly charge at a different price.
func TestBuiltChargerInheritsCityChargingDefaults(t *testing.T) {
	cfg := &CityConfig{
		Name: "built charger", GridSize: 5, TickMS: 100,
		Layout: []string{
			"WWWWW",
			"WDRRW",
			"WRRRW",
			"WRRRW",
			"WWWWW",
		},
		Legend:   map[string]string{"W": "water", "R": "road", "D": "depot"},
		Items:    map[string]string{},
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
		Charging: ChargingConfig{DepotRate: 5, ChargerRate: 3, ChargerPricePerTick: 1},
	}
	city, err := NewCity(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"id":"ch_built","pos":{"x":3,"y":2},"capacity":1}`)
	if err := city.applyEffects([]GrowthEffect{{Type: EffectAddCharger, Entities: []json.RawMessage{raw}}}); err != nil {
		t.Fatal(err)
	}
	ch, ok := city.ChargerAt(Position{3, 2})
	if !ok {
		t.Fatal("built charger missing from the entity list")
	}
	if ch.Rate != 3 || ch.PricePerTick != 1 {
		t.Fatalf("built charger = rate %d price $%d, want 3 and $1", ch.Rate, ch.PricePerTick)
	}
}

// TestChargingSessionSurvivesSave makes the new car fields part of the save
// contract: reloading mid-charge must not erase the bill or charge it twice.
func TestChargingSessionSurvivesSave(t *testing.T) {
	w := paidChargerWorld(t, 2, 1, 6)
	w.Advance()
	w.Advance()

	saved := w.ExportState()
	if saved.Cars[0].ChargeDue != 2 || saved.Cars[0].ChargeUnits != 4 {
		t.Fatalf("save lost the open session: %+v", saved.Cars[0])
	}

	// Rebuild the same city, then restore the state into it.
	restored := paidChargerWorld(t, 2, 1, 6)
	if err := restored.ImportState(saved); err != nil {
		t.Fatal(err)
	}
	restoredCar := restored.Cars[0]
	restored.Advance()

	if restoredCar.Battery != 6 || restoredCar.ChargeDue != 0 {
		t.Fatalf("restored charge: battery=%d due=$%d", restoredCar.Battery, restoredCar.ChargeDue)
	}
	if len(restored.Ledger.Cash) != 1 || restored.Ledger.Cash[0].Amount != -3 {
		t.Fatalf("restored statement = %+v, want one -$3 line", restored.Ledger.Cash)
	}
}

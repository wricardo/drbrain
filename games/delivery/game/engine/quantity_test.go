package engine

import (
	"path/filepath"
	"testing"
)

// quantityWorld builds a small city with a configurable fleet and quantity
// bands, so the roll can be tested without driving a real playthrough.
func quantityWorld(t *testing.T, cap int, bands []QuantityBandConfig) *World {
	t.Helper()
	rows := []string{
		"WWWWWWW",
		"WRRRRRW",
		"WDKRKHR",
		"WRRRRRW",
		"WWWWWWW",
		"WWWWWWW",
		"WWWWWWW",
	}
	cfg := completeConfig(&CityConfig{
		Name:     "qty",
		GridSize: 7,
		TickMS:   100,
		Layout:   rows,
		Legend: map[string]string{
			"W": "water", "R": "road", "D": "depot", "K": "kitchen", "H": "house",
		},
		Items:    map[string]string{"pizza": "Pizza"},
		Depots:   []DepotConfig{{ID: "d1", Pos: Position{1, 2}, ChargeSlots: 2}},
		Kitchens: []KitchenConfig{{ID: "k1", Name: "Kitchen", Pos: Position{2, 2}, Menu: []string{"pizza"}}},
		Customers: []CustomerConfig{{
			ID: "c1", Name: "Cust", Pos: Position{5, 2}, Demands: []string{"pizza"},
			OrderIntervalTicks: 20,
		}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Capacity: cap, MaxBattery: 20, StepCost: 1, Price: 0},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Name: "S1", Pos: Position{1, 2}}},
		Orders: OrdersConfig{
			MaxActiveOrders:         4,
			TargetOrdersPerCustomer: 1.0,
			RampTicks:               100,
			MinSpawnGapTicks:        1,
			DeadlineSlack:           3.0,
			RewardBase:              map[string]int{"pizza": 40},
			QuantityBands:           bands,
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

var testBands = []QuantityBandConfig{
	{MinSize: 1, MaxSize: 3, FromProgress: 0, Weight: 70},
	{MinSize: 4, MaxSize: 10, FromProgress: 0.2, Weight: 25},
	{MinSize: 12, MaxSize: 24, FromProgress: 0.4, Weight: 6},
	{MinSize: 30, MaxSize: 40, FromProgress: 0.8, Weight: 8},
}

// TestQuantityMayExceedTheFleet is the demand signal: a city can offer a job
// bigger than anything in the current fleet, and the assignment path will
// refuse it until the player upgrades. That pending order is what makes the
// van/truck ladder a decision rather than an optional purchase.
func TestQuantityMayExceedTheFleet(t *testing.T) {
	w := quantityWorld(t, 4, testBands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}
	w.Tick = 40

	sawOversized := false
	for seed := uint64(0); seed < 3000; seed++ {
		w.Seed = seed
		q := w.rollQuantity(cu)
		if q < 1 {
			t.Fatalf("rolled a non-positive quantity %d", q)
		}
		if q > 4 {
			sawOversized = true
		}
	}
	if !sawOversized {
		t.Fatal("a 4-box fleet never saw an oversized order; demand is still fleet-gated")
	}
}

// TestQuantityBandsAreProgressGated proves the escalation: a new player only
// sees scooter-sized work, and the larger bands open as a percentage of the
// tick ramp rather than at chapter boundaries.
func TestQuantityBandsAreProgressGated(t *testing.T) {
	w := quantityWorld(t, 40, testBands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}

	maxAt := func(progress float64) int {
		w.Tick = int64(progress * float64(w.ordersCfg.RampTicks))
		biggest := 0
		for seed := uint64(0); seed < 3000; seed++ {
			w.Seed = seed
			if q := w.rollQuantity(cu); q > biggest {
				biggest = q
			}
		}
		return biggest
	}

	if got := maxAt(0); got > 3 {
		t.Errorf("0%% ramp saw a %d-box order, want at most 3", got)
	}
	if got := maxAt(0.2); got < 4 || got > 10 {
		t.Errorf("20%% ramp largest = %d, want in 4..10", got)
	}
	if got := maxAt(0.4); got < 12 || got > 24 {
		t.Errorf("40%% ramp largest = %d, want in 12..24", got)
	}
	if got := maxAt(0.7); got > 24 {
		t.Errorf("70%% ramp saw a %d-box truck order before the late band", got)
	}
	if got := maxAt(0.8); got < 30 {
		t.Errorf("80%% ramp largest = %d, want at least 30", got)
	}
}

// TestEarlyWeightKeepsBandsAvailableBeforeTheirGate proves the floor contract:
// a band with a positive EarlyWeight can roll from the first tick at a small
// share, and its full Weight still takes over at FromProgress.
func TestEarlyWeightKeepsBandsAvailableBeforeTheirGate(t *testing.T) {
	bands := []QuantityBandConfig{
		{MinSize: 1, MaxSize: 3, Weight: 40},
		{MinSize: 30, MaxSize: 40, FromProgress: 0.5, EarlyWeight: 1, Weight: 1000},
	}
	w := quantityWorld(t, 40, bands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}

	counts := func(progress float64) (small, big int) {
		w.Tick = int64(progress * float64(w.ordersCfg.RampTicks))
		for seed := uint64(0); seed < 3000; seed++ {
			w.Seed = seed
			if q := w.rollQuantity(cu); q >= 30 {
				big++
			} else {
				small++
			}
		}
		return small, big
	}

	small0, big0 := counts(0)
	if big0 == 0 {
		t.Fatal("early_weight band never rolled at progress 0; the range is still gated")
	}
	if big0*20 > small0 {
		t.Fatalf("early_weight band is %d/%d rolls at progress 0; want a rare floor, under 5%%", big0, small0+big0)
	}

	_, big1 := counts(0.5)
	if big1 < big0*10 {
		t.Fatalf("gate did not escalate the band: %d rolls before, %d after", big0, big1)
	}
}

// TestQuantityBandKeepsItsFullRange proves ramp eligibility does not clamp a
// band down to the current hold: when the 12-24 band is live it can ask for 24
// boxes even if the player has only a scooter.
func TestQuantityBandKeepsItsFullRange(t *testing.T) {
	w := quantityWorld(t, 4, testBands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}
	w.Tick = 40

	biggest := 0
	for seed := uint64(0); seed < 4000; seed++ {
		w.Seed = seed
		if q := w.rollQuantity(cu); q > biggest {
			biggest = q
		}
	}
	if biggest <= 4 || biggest > 24 {
		t.Fatalf("largest 12-24 band roll = %d, want in 5..24", biggest)
	}
}

// TestNoBandsMeansOneBox keeps a map without a quantity table behaving exactly
// as it did before quantities existed.
func TestNoBandsMeansOneBox(t *testing.T) {
	w := quantityWorld(t, 40, nil)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}
	for tick := int64(0); tick < 200; tick++ {
		w.Tick = tick
		if q := w.rollQuantity(cu); q != 1 {
			t.Fatalf("quantity = %d with no bands, want 1", q)
		}
	}
}

// TestBulkRewardIsSublinear is the economic guard. Linear pay per box makes
// bulk strictly better, which turns "which order do I take?" into a non-question.
func TestBulkRewardIsSublinear(t *testing.T) {
	w := quantityWorld(t, 40, testBands)
	kitchen, customer := Position{2, 2}, Position{5, 2}

	one := w.rewardFor("pizza", kitchen, customer, 1)
	ten := w.rewardFor("pizza", kitchen, customer, 10)
	forty := w.rewardFor("pizza", kitchen, customer, 40)

	if ten <= one {
		t.Fatalf("10 boxes pay %d, not more than 1 box at %d", ten, one)
	}
	if forty <= ten {
		t.Fatalf("40 boxes pay %d, not more than 10 boxes at %d", forty, ten)
	}
	// Per-box value must fall as the order grows.
	if forty/40 >= one {
		t.Errorf("per-box pay at 40 boxes is %d, not below the single-box rate %d", forty/40, one)
	}
}

// TestBoxLoadTracksPickupAndDropoff proves the hold accounting does not drift:
// what goes in comes out, including when an order expires mid-transit.
func TestBoxLoadTracksPickupAndDropoff(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 3)
	if err != nil {
		t.Fatal(err)
	}
	car := w.Cars[0]
	car.Class.Capacity = 20

	var kitchen Kitchen
	for _, k := range w.City.Kitchens {
		if k.ID == "k_pizza" {
			kitchen = k
		}
	}
	if kitchen.ID == "" {
		t.Fatal("no k_pizza")
	}

	o := &Order{
		ID: "ord_x", Item: "pizza", KitchenID: kitchen.ID, CustomerID: "c_anna",
		Quantity: 5, State: OrderAssigned, AssignedCar: car.ID,
		DeadlineTick: 100000, Reward: 100,
	}
	w.Orders = append(w.Orders, o)

	car.Pos = kitchen.Pos
	w.resolveTileInteractions(car)
	if o.State != OrderPickedUp {
		t.Fatalf("state = %s, want picked_up", o.State)
	}
	if car.Load() != 5 {
		t.Fatalf("load = %d, want 5", car.Load())
	}

	// Expiry mid-transit must free the boxes, or a car silently loses hold
	// space for the rest of the session.
	w.Tick = o.DeadlineTick + 1
	w.expireOrders()
	if car.Load() != 0 {
		t.Fatalf("load after expiry = %d, want 0", car.Load())
	}
	if len(car.Cargo) != 0 {
		t.Fatalf("cargo after expiry = %v, want empty", car.Cargo)
	}
}

// TestShippedCitiesDoNotAskForWhatTheStartingFleetCannotCarry holds every shipped
// city to the rule the truck band already followed: a band whose smallest order
// does not fit the starting cars must not be in the draw before its gate.
//
// The cities start with one scooter (4 boxes) and $0, and a van costs $600. An
// early_weight on the 5 to 10 or 13 to 24 box band let an order of that size roll
// in the first 400 ticks, when nothing that could carry it could yet be bought
// inside its 25 to 35 tick window, so it could only expire (about one run in
// two). Both gates sit after the point a good run has earned a van's price.
func TestShippedCitiesDoNotAskForWhatTheStartingFleetCannotCarry(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "maps", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped cities found: %v", err)
	}
	for _, path := range paths {
		_, cfg, err := CityFromFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		startCap := 0
		for _, c := range cfg.InitialCars {
			startCap = max(startCap, cfg.CarClasses[c.Class].Capacity)
		}
		for _, b := range cfg.Orders.QuantityBands {
			if b.MinSize > startCap && b.EarlyWeight != 0 {
				t.Errorf("%s: band %d-%d boxes has early_weight %d but the starting fleet carries %d",
					filepath.Base(path), b.MinSize, b.MaxSize, b.EarlyWeight, startCap)
			}
		}
	}
}

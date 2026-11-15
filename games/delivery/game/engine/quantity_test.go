package engine

import (
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
	cfg := &CityConfig{
		Name:     "qty",
		GridSize: 7,
		TickMS:   100,
		Layout:   rows,
		Legend: map[string]string{
			"W": "water", "R": "road", "D": "depot", "K": "kitchen", "H": "house",
		},
		Items:    map[string]string{"pizza": "Pizza"},
		Depots:   []DepotConfig{{ID: "d1", Pos: Position{1, 2}, Capacity: 4, ChargeSlots: 2}},
		Kitchens: []KitchenConfig{{ID: "k1", Name: "Kitchen", Pos: Position{2, 2}, Menu: []string{"pizza"}}},
		Customers: []CustomerConfig{{
			ID: "c1", Name: "Cust", Pos: Position{5, 2}, Demands: []string{"pizza"},
			OrderIntervalTicks: 20,
		}},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Speed: 1, Capacity: cap, MaxBattery: 20, StepCost: 1, Price: 0},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Name: "S1", Pos: Position{1, 2}}},
		Orders: OrdersConfig{
			MaxActiveOrders:    4,
			TargetOrdersPerCar: 1.0,
			MinSpawnGapTicks:   1,
			DeadlineSlack:      3.0,
			RewardBase:         map[string]int{"pizza": 40},
			QuantityBands:      bands,
		},
		Charging: ChargingConfig{DepotRate: 5, ChargerRate: 2},
	}
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
	{MinSize: 1, MaxSize: 3, FromChapter: 0, Weight: 70},
	{MinSize: 4, MaxSize: 10, FromChapter: 1, Weight: 25},
	{MinSize: 12, MaxSize: 24, FromChapter: 2, Weight: 6},
	{MinSize: 30, MaxSize: 40, FromChapter: 3, Weight: 8},
}

// TestQuantityNeverExceedsTheFleetIsTheWholePoint is the rule that keeps order
// sizes from becoming a broken promise.
//
// A forty-box order offered to a four-box scooter is not difficulty, it is a
// job the player cannot accept: the assignment check would reject it and the
// pickup would silently refuse it. So the spawner never rolls a band whose
// smallest size does not fit the largest hold in the fleet.
func TestQuantityNeverExceedsTheFleet(t *testing.T) {
	for _, cap := range []int{4, 12, 40} {
		w := quantityWorld(t, cap, testBands)
		var cu Customer
		for _, c := range w.City.Customers {
			cu = c
		}
		for tick := int64(0); tick < 2000; tick++ {
			w.Tick = tick
			q := w.rollQuantity(cu)
			if q > cap {
				t.Fatalf("capacity %d: rolled %d boxes at tick %d", cap, q, tick)
			}
			if q < 1 {
				t.Fatalf("capacity %d: rolled a non-positive quantity %d", cap, q)
			}
		}
	}
}

// TestQuantityBandsAreChapterGated proves the escalation: a new player only
// ever sees singles, and bigger orders arrive as the city grows.
func TestQuantityBandsAreChapterGated(t *testing.T) {
	w := quantityWorld(t, 40, testBands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}

	maxAt := func(chapter int) int {
		w.Ledger.BuildsCompleted = chapter
		biggest := 0
		for tick := int64(0); tick < 3000; tick++ {
			w.Tick = tick
			if q := w.rollQuantity(cu); q > biggest {
				biggest = q
			}
		}
		return biggest
	}

	if got := maxAt(0); got > 3 {
		t.Errorf("chapter 0 saw a %d-box order, want at most 3", got)
	}
	if got := maxAt(1); got < 4 || got > 10 {
		t.Errorf("chapter 1 largest = %d, want in 4..10", got)
	}
	if got := maxAt(2); got < 12 || got > 24 {
		t.Errorf("chapter 2 largest = %d, want in 12..24", got)
	}
	if got := maxAt(3); got < 30 {
		t.Errorf("chapter 3 largest = %d, want at least 30", got)
	}
}

// TestQuantityBandIsClampedToTheFleetNotDropped covers the middle case: a van
// holds twelve boxes, so a 12-24 band should still produce twelve-box work
// rather than disappearing entirely.
func TestQuantityBandIsClampedToTheFleet(t *testing.T) {
	w := quantityWorld(t, 12, testBands)
	var cu Customer
	for _, c := range w.City.Customers {
		cu = c
	}
	w.Ledger.BuildsCompleted = 2

	sawTwelve := false
	for tick := int64(0); tick < 4000; tick++ {
		w.Tick = tick
		q := w.rollQuantity(cu)
		if q > 12 {
			t.Fatalf("rolled %d boxes for a 12-box fleet", q)
		}
		if q == 12 {
			sawTwelve = true
		}
	}
	if !sawTwelve {
		t.Error("the 12-24 band produced nothing for a 12-box fleet; it should clamp, not vanish")
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

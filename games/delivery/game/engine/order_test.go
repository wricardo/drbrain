package engine

import (
	"testing"
)

// orderWorld builds a small city with one kitchen and one customer close
// enough that a scooter can complete the round trip.
func orderWorld(t *testing.T) *World {
	t.Helper()

	rows := []string{
		"WWWWWWW",
		"WDKRRRW",
		"WRRRRRW",
		"WRHRRRW",
		"WRRRRRW",
		"WRRRRRW",
		"WWWWWWW",
	}
	cfg := &CityConfig{
		Name:     "orders",
		GridSize: 7,
		TickMS:   100,
		Layout:   rows,
		Legend: map[string]string{
			"W": "water", "R": "road", "D": "depot", "K": "kitchen", "H": "house",
		},
		Items:  map[string]string{"pizza": "Pizza"},
		Depots: []DepotConfig{{ID: "d1", Pos: Position{1, 1}, Capacity: 4, ChargeSlots: 2}},
		Kitchens: []KitchenConfig{
			{ID: "k1", Name: "Kitchen", Pos: Position{2, 1}, Menu: []string{"pizza"}},
		},
		Customers: []CustomerConfig{
			{ID: "c1", Name: "Cust", Pos: Position{2, 3}, Demands: []string{"pizza"},
				OrderIntervalTicks: 20, JitterTicks: 0},
		},
		CarClasses: map[string]CarClassConfig{
			"scooter": {Speed: 1, Capacity: 2, MaxBattery: 20, StepCost: 1, Price: 0},
		},
		InitialCars: []InitialCarConfig{{Class: "scooter", Name: "S1", Pos: Position{1, 1}}},
		Orders: OrdersConfig{
			MaxActiveOrders:        3,
			TargetOrdersPerCar:     1.0,
			MinSpawnGapTicks:       1,
			DeadlineSlack:          3.0,
			DeadlineSlackByChapter: []float64{3.0, 3.0, 3.0, 3.0, 3.0},
			RewardBase:             map[string]int{"pizza": 50},
			DistanceBonusPerTile:   2,
			LatePayMultiplier:      0.5,
			ReputationPerDelivery:  0.02,
			ReputationPerLate:      0.05,
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

// firstOrder returns the only open order, failing the test if there is none.
func firstOrder(t *testing.T, w *World) *Order {
	t.Helper()
	open := w.OpenOrders()
	if len(open) == 0 {
		t.Fatal("no open order")
	}
	return open[0]
}

// driveTo walks a car to a target using the world's own move rules.
func driveTo(t *testing.T, w *World, car *Car, target Position) {
	t.Helper()
	for range 200 {
		if car.Pos == target {
			return
		}
		dir := firstStepToward(w, car.Pos, target)
		if dir == "" {
			t.Fatalf("no route from %s to %s", car.Pos, target)
		}
		if err := w.QueueMove(car.ID, dir); err != nil {
			t.Fatalf("queue: %v", err)
		}
		w.Advance()
	}
	t.Fatalf("car did not reach %s", target)
}

// firstStepToward is a test-local BFS so the test does not depend on the CLI's
// copy.
func firstStepToward(w *World, from, to Position) Direction {
	type qe struct {
		p     Position
		first Direction
	}
	queue := []qe{{p: from}}
	seen := map[Position]bool{from: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := cur.p.Neighbour(d)
			if !ok || seen[n] || !w.City.Walkable(n) {
				continue
			}
			f := cur.first
			if cur.p == from {
				f = d
			}
			if n == to {
				return f
			}
			seen[n] = true
			queue = append(queue, qe{p: n, first: f})
		}
	}
	return ""
}

func TestOrderSpawnsOnFirstTick(t *testing.T) {
	w := orderWorld(t)
	w.Advance()

	if got := w.ActiveOrderCount(); got != 1 {
		t.Fatalf("active orders = %d, want 1 (forced first order)", got)
	}
	o := firstOrder(t, w)
	if o.Item != "pizza" {
		t.Errorf("item = %q, want pizza", o.Item)
	}
	if o.KitchenID != "k1" || o.CustomerID != "c1" {
		t.Errorf("order points at %s/%s, want k1/c1", o.KitchenID, o.CustomerID)
	}
	if o.DeadlineTick <= 0 {
		t.Error("deadline not set")
	}
}

func TestPickupOnKitchenAndDeliverOnCustomer(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if o.State != OrderAssigned {
		t.Fatalf("state after assign = %s, want assigned", o.State)
	}

	// Drive to the kitchen: pickup is automatic on arrival.
	driveTo(t, w, car, Position{2, 1})
	if o.State != OrderPickedUp {
		t.Fatalf("state after reaching kitchen = %s, want picked_up", o.State)
	}
	if len(car.Cargo) != 1 || car.Cargo[0] != o.ID {
		t.Fatalf("cargo = %v, want [%s]", car.Cargo, o.ID)
	}

	// Drive to the customer: delivery completes on arrival.
	driveTo(t, w, car, Position{2, 3})
	if o.State != OrderDone {
		t.Fatalf("state after reaching customer = %s, want done", o.State)
	}
	if len(car.Cargo) != 0 {
		t.Fatalf("cargo not emptied: %v", car.Cargo)
	}
	if w.Ledger.Deliveries != 1 {
		t.Errorf("deliveries = %d, want 1", w.Ledger.Deliveries)
	}
	if w.Ledger.Money != o.Reward {
		t.Errorf("money = %d, want the full reward %d", w.Ledger.Money, o.Reward)
	}
	if w.Ledger.MoneyEarned != o.Reward {
		t.Errorf("moneyEarned = %d, want %d", w.Ledger.MoneyEarned, o.Reward)
	}
}

func TestUnassignedOrderCannotBePickedUp(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	// Drive to the kitchen WITHOUT assigning.
	driveTo(t, w, car, Position{2, 1})
	if o.State != OrderPending {
		t.Fatalf("unassigned order changed state to %s", o.State)
	}
	if len(car.Cargo) != 0 {
		t.Fatalf("unassigned order was collected: %v", car.Cargo)
	}
}

func TestAssignToFullCarIsRejected(t *testing.T) {
	w := orderWorld(t)
	// Force a full cargo hold. Capacity is measured in boxes, so a fake order
	// id alone is not enough: the load has to actually fill the hold.
	car := w.Cars[0]
	car.Cargo = []string{"fake1", "fake2"}
	car.cargoBoxes = car.Class.Capacity

	w.Advance()
	// Find an order that the full car cannot take.
	for _, o := range w.OpenOrders() {
		if err := w.AssignOrder(o.ID, car.ID); err == nil {
			t.Fatal("expected assignment to a full car to fail")
		}
	}
}

// TestAssignRejectsOrderBiggerThanHold is the size gate. It is the rule that
// makes a truck a decision rather than a stat: forty boxes are refused by a
// scooter, and the refusal is an error the player sees rather than a silent
// no-op at the kitchen.
func TestAssignRejectsOrderBiggerThanHold(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]
	o.Quantity = car.Class.Capacity + 1

	err := w.AssignOrder(o.ID, car.ID)
	if err == nil {
		t.Fatalf("%d boxes accepted by a %d-box car", o.Quantity, car.Class.Capacity)
	}
	if o.State != OrderPending {
		t.Errorf("rejected assignment still changed state to %s", o.State)
	}
	if o.AssignedCar != "" {
		t.Errorf("rejected assignment still named a car: %q", o.AssignedCar)
	}
}

// TestOversizeOrderIsNotPickedUp pins the failure mode that a size gate can
// otherwise hide: an order assigned somehow and too big must not be silently
// skipped forever. It is not collected, and the car's load stays untouched.
func TestOversizeOrderIsNotPickedUp(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatal(err)
	}
	o.Quantity = car.Class.Capacity + 1 // shrink the car out from under it
	driveTo(t, w, car, Position{2, 1})

	if o.State != OrderAssigned {
		t.Fatalf("oversize order state = %s, want still assigned", o.State)
	}
	if car.Load() != 0 {
		t.Errorf("car load = %d after refusing an oversize order, want 0", car.Load())
	}
}

func TestExpiredOrderAppliesPenaltyAndClearsCargo(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatal(err)
	}
	driveTo(t, w, car, Position{2, 1}) // pick up
	if o.State != OrderPickedUp {
		t.Fatalf("expected picked_up, got %s", o.State)
	}

	// Jump past the deadline and let a tick expire it.
	w.Tick = o.DeadlineTick + 1
	w.Advance()

	if o.State != OrderExpired {
		t.Fatalf("state = %s, want expired", o.State)
	}
	if len(car.Cargo) != 0 {
		t.Fatalf("cargo should be cleared on expiry, got %v", car.Cargo)
	}
	if w.Ledger.ExpiredOrders != 1 {
		t.Errorf("expired counter = %d, want 1", w.Ledger.ExpiredOrders)
	}
	if w.Ledger.ExpiredInTransit != 1 {
		t.Errorf("in-transit expiry diagnostic = %d, want 1", w.Ledger.ExpiredInTransit)
	}
	if w.Ledger.Reputation >= 3.0 {
		t.Errorf("reputation did not fall on expiry: %f", w.Ledger.Reputation)
	}
}

func TestLateDeliveryPaysReducedButCounts(t *testing.T) {
	w := orderWorld(t)
	// Set the knobs explicitly so this test does not depend on defaults.
	w.ordersCfg.LatePayMultiplier = 0.5
	w.ordersCfg.ReputationPerLate = 0.05
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatal(err)
	}
	driveTo(t, w, car, Position{2, 1}) // pick up
	if o.State != OrderPickedUp {
		t.Fatalf("expected picked_up, got %s", o.State)
	}

	// Push the deadline so the NEXT tick makes it late rather than expired:
	// expiry is checked against Tick > Deadline, delivery against the same, so
	// driving onto the customer on the same tick is the "late but delivered"
	// case. Set the deadline exactly at the current tick and deliver now.
	o.DeadlineTick = w.Tick - 2
	// Deliver by driving, but expiry runs first in a tick, so bypass by
	// completing through the public path used by the tick.
	car.Pos = Position{2, 3}
	w.resolveTileInteractions(car)

	if o.State != OrderDone {
		t.Fatalf("state = %s, want done", o.State)
	}
	if !o.Late {
		t.Error("order not marked late")
	}
	if o.Paid >= o.Reward {
		t.Errorf("late pay %d should be below full reward %d", o.Paid, o.Reward)
	}
	if w.Ledger.Deliveries != 0 {
		t.Errorf("late delivery counted as on-time (deliveries=%d)", w.Ledger.Deliveries)
	}
	if w.Ledger.LateDeliveries != 1 {
		t.Errorf("late counter = %d, want 1", w.Ledger.LateDeliveries)
	}
}

func TestReputationIsClampedToScale(t *testing.T) {
	w := orderWorld(t)
	w.Ledger.Reputation = 99
	w.clampReputation()
	if w.Ledger.Reputation != MaxReputation {
		t.Fatalf("reputation = %f, want clamped to %f", w.Ledger.Reputation, MaxReputation)
	}
	w.Ledger.Reputation = -99
	w.clampReputation()
	if w.Ledger.Reputation != 0 {
		t.Fatalf("reputation = %f, want clamped to 0", w.Ledger.Reputation)
	}
}

func TestTargetActiveScalesWithFleetSize(t *testing.T) {
	w := orderWorld(t)
	// One car and a ratio of 1.0 => target 1. Deliberately NOT derived from
	// capacity: this scooter holds two boxes, and a box-based target would ask
	// for two concurrent orders from a player who can only drive one at a time.
	if got := w.targetActive(); got != 1 {
		t.Fatalf("targetActive = %d, want 1", got)
	}
	// A stranded car is not on the road and must not count toward the target.
	w.Cars[0].Battery = 0
	w.Cars[0].Pos = Position{5, 5}
	if got := w.targetActive(); got != 1 {
		t.Fatalf("targetActive with stranded fleet = %d, want floor of 1", got)
	}
	// Three cars on the road => three open orders.
	w2 := orderWorld(t)
	w2.Cars[0].Battery = 10
	for i := range 2 {
		car, err := w2.BuyCar("scooter", "d1")
		if err != nil {
			t.Fatalf("buy car %d: %v", i, err)
		}
		_ = car
	}
	if got := w2.targetActive(); got != 3 {
		t.Fatalf("targetActive with 3 cars = %d, want 3", got)
	}
}

func TestSpawnRespectsMaxActiveOrders(t *testing.T) {
	w := orderWorld(t)
	for range 40 {
		w.Advance()
	}
	if got := w.ActiveOrderCount(); got > w.ordersCfg.MaxActiveOrders {
		t.Fatalf("active orders %d exceeds max %d", got, w.ordersCfg.MaxActiveOrders)
	}
	if w.Ledger.OrdersSpawned == 0 {
		t.Fatal("nothing spawned at all")
	}
}

func TestSpawnIsDeterministicForSeed(t *testing.T) {
	run := func() []string {
		w := orderWorld(t)
		for range 60 {
			w.Advance()
		}
		var ids []string
		for _, o := range w.Orders {
			ids = append(ids, o.ID+":"+o.Item+":"+o.CustomerID)
		}
		return ids
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("spawn counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("spawn %d differs: %q vs %q", i, a[i], b[i])
		}
	}
}

func TestNoSpawnForItemWithNoKitchen(t *testing.T) {
	// A customer demanding sushi with no sushi kitchen must not produce orders,
	// and must not crash: the spawner reports it via events instead.
	w := orderWorld(t)
	w.City.Customers = append(w.City.Customers, Customer{
		ID: "c_sushi", Name: "Sushi Fan", Pos: Position{4, 5},
		Demands: []string{"sushi"}, OrderIntervalTicks: 5,
	})
	w.City.Items["sushi"] = "Sushi"

	for range 50 {
		w.Advance()
	}
	for _, o := range w.Orders {
		if o.Item == "sushi" {
			t.Fatalf("spawned an unfulfillable sushi order: %+v", o)
		}
	}
}

func TestBuildRequiresUnlockAndMoney(t *testing.T) {
	w := orderWorld(t)
	w.builds = []BuildConfig{{
		ID: "b1", Title: "Charger", Cost: 100,
		Effects: []BuildEffect{{Type: EffectAddCharger, Entities: nil}},
	}}

	// Locked.
	if _, err := w.Build("b1"); err == nil {
		t.Fatal("expected locked build to be rejected")
	}
	w.permitted["b1"] = true
	// Unlocked but broke.
	if _, err := w.Build("b1"); err == nil {
		t.Fatal("expected unaffordable build to be rejected")
	}
	w.Ledger.Money = 150
	res, err := w.Build("b1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.MoneyLeft != 50 {
		t.Errorf("money left = %d, want 50", res.MoneyLeft)
	}
	if w.Ledger.BuildsCompleted != 1 {
		t.Errorf("builds completed = %d, want 1", w.Ledger.BuildsCompleted)
	}
	// Cannot build twice.
	if _, err := w.Build("b1"); err == nil {
		t.Fatal("expected double build to be rejected")
	}
}

func TestUnlockGrantsBuildPermission(t *testing.T) {
	w := orderWorld(t)
	w.unlocks = []UnlockConfig{{
		ID: "u1", Title: "First", Requires: Requirement{Deliveries: new(1)},
		GrantsBuild: []string{"b1"},
	}}
	if w.permitted["b1"] {
		t.Fatal("build permitted before unlock earned")
	}
	w.Ledger.Deliveries = 1
	w.evalProgression()

	if !w.permitted["b1"] {
		t.Fatal("unlock did not grant build permission")
	}
	if !w.unlocks[0].Earned {
		t.Fatal("unlock not marked earned")
	}
}

func TestAchievementGrantsMoneyOnce(t *testing.T) {
	w := orderWorld(t)
	w.achievements = []AchievementConfig{{
		ID: "a1", Title: "First", Requires: Requirement{Deliveries: new(1)},
		GrantMoney: 100,
	}}
	w.Ledger.Deliveries = 1
	w.evalProgression()
	first := w.Ledger.Money
	if first != 100 {
		t.Fatalf("money after achievement = %d, want 100", first)
	}
	// Evaluating again must not pay twice.
	w.evalProgression()
	if w.Ledger.Money != first {
		t.Fatalf("achievement paid twice: %d -> %d", first, w.Ledger.Money)
	}
}

func TestBuyCarSpendsMoneyAndRequiresClass(t *testing.T) {
	w := orderWorld(t)
	w.carClasses["van"] = CarClassConfig{
		Speed: 1, Capacity: 3, MaxBattery: 30, StepCost: 1, Price: 500,
	}

	if _, err := w.BuyCar("van", "d1"); err == nil {
		t.Fatal("expected unaffordable van to be rejected")
	}
	// A priced class needs an explicit grant before it can be bought.
	w.permitted["van"] = true
	w.buyableClasses["van"] = true
	if _, err := w.BuyCar("van", "d1"); err == nil {
		t.Fatal("expected van to still be unaffordable at $0")
	}
	w.Ledger.Money = 600
	car, err := w.BuyCar("van", "d1")
	if err != nil {
		t.Fatalf("buy van: %v", err)
	}
	if car.Class.Capacity != 3 {
		t.Errorf("van capacity = %d, want 3", car.Class.Capacity)
	}
	if w.Ledger.Money != 100 {
		t.Errorf("money after purchase = %d, want 100", w.Ledger.Money)
	}
}

// intPtr was removed in favour of new(1) at the call sites (Go 1.27 allocates
// an addressable value directly, with no helper frame).

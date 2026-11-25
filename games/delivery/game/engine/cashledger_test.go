package engine

import "testing"

// checkCashChain verifies the two invariants a statement must never break:
// seqs increase, and each balance is the previous balance plus the signed
// amount. It starts from the second entry because a test that seeds Money
// directly has no opening line to chain from.
func checkCashChain(t *testing.T, w *World) {
	t.Helper()
	cash := w.Ledger.Cash
	for i := 1; i < len(cash); i++ {
		if cash[i].Seq <= cash[i-1].Seq {
			t.Fatalf("seq did not increase: %d then %d", cash[i-1].Seq, cash[i].Seq)
		}
		if want := cash[i-1].Balance + cash[i].Amount; cash[i].Balance != want {
			t.Fatalf("entry %d balance = %d, want %d (%d + %d)",
				cash[i].Seq, cash[i].Balance, want, cash[i-1].Balance, cash[i].Amount)
		}
	}
}

// TestCashLedgerDeliveryCarriesCarAndOrder is the headline contract: the money
// a delivery pays in is a statement line tagged with the car that delivered it
// and the order that paid.
func TestCashLedgerDeliveryCarriesCarAndOrder(t *testing.T) {
	w := orderWorld(t)
	// Full standing, so the row is the quoted reward rather than a reputation
	// discount; the ladder has its own test.
	w.Ledger.Reputation = w.maxReputation
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]

	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	driveTo(t, w, car, Position{2, 1})
	driveTo(t, w, car, Position{2, 3})

	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %d, want just the delivery: %+v", len(w.Ledger.Cash), w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashDelivery {
		t.Errorf("kind = %s, want %s", e.Kind, CashDelivery)
	}
	if e.Amount != o.Reward || e.Balance != w.Ledger.Money {
		t.Errorf("amount/balance = %d/%d, want %d/%d", e.Amount, e.Balance, o.Reward, w.Ledger.Money)
	}
	if e.CarID != car.ID || e.CarName != car.Name {
		t.Errorf("car = %q/%q, want %q/%q", e.CarID, e.CarName, car.ID, car.Name)
	}
	if e.OrderID != o.ID {
		t.Errorf("order = %q, want %q", e.OrderID, o.ID)
	}
	if e.Memo != "Pizza to Cust" {
		t.Errorf("memo = %q, want the item and customer", e.Memo)
	}
	checkCashChain(t, w)
}

// TestCashLedgerLateDeliveryIsItsOwnKind keeps a late payment distinguishable
// from a full one: the statement must show why the amount is short.
func TestCashLedgerLateDeliveryIsItsOwnKind(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]
	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	driveTo(t, w, car, Position{2, 1})
	// Stand one step from the customer, then make the order overdue. The final
	// step delivers in the move phase, before the expiry sweep sees the missed
	// deadline, so this is a late delivery rather than an expiry.
	driveTo(t, w, car, Position{2, 2})
	o.DeadlineTick = w.Tick - 1
	driveTo(t, w, car, Position{2, 3})

	if o.State != OrderDone || !o.Late {
		t.Fatalf("order = %s late=%v, want done and late", o.State, o.Late)
	}
	e := w.Ledger.Cash[len(w.Ledger.Cash)-1]
	if e.Kind != CashLateDelivery {
		t.Errorf("kind = %s, want %s", e.Kind, CashLateDelivery)
	}
	if e.Amount != o.Paid {
		t.Errorf("amount = %d, want the late payment %d", e.Amount, o.Paid)
	}
	checkCashChain(t, w)
}

// TestCashLedgerUpkeepIsPerCarAndParkedIsFree proves the cost is attributed to
// the car that incurred it and that parking produces no statement row.
func TestCashLedgerUpkeepIsPerCarAndParkedIsFree(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0]
	w.Ledger.Money = 100
	car.Pos = Position{4, 1} // off the depot, so the interval bills it

	for range 10 {
		w.AdvanceNoPause()
	}
	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %d, want one upkeep row: %+v", len(w.Ledger.Cash), w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashUpkeep || e.Amount != -1 || e.Balance != 99 {
		t.Errorf("upkeep entry = %+v, want -1 with balance 99", e)
	}
	if e.CarID != car.ID || e.CarName != car.Name {
		t.Errorf("upkeep car = %q/%q, want %q/%q", e.CarID, e.CarName, car.ID, car.Name)
	}

	car.Pos = Position{1, 1} // back on the depot
	for range 10 {
		w.AdvanceNoPause()
	}
	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("parked car added a cash row: %+v", w.Ledger.Cash)
	}
	checkCashChain(t, w)
}

// TestCashLedgerUpkeepSplitsAcrossCarsTaggedPerVehicle is the "per car" half
// of a fleet bill: two cars away from the depot produce two rows, each tagged
// to its own vehicle.
func TestCashLedgerUpkeepSplitsAcrossCarsTaggedPerVehicle(t *testing.T) {
	w := fleetWorld(t, 10)
	scooter := w.Cars[0]
	scooter.Pos = Position{4, 1}
	van, err := NewCar("van_1", "V", "van", Position{5, 1}, map[string]CarClassConfig{
		"van": {Capacity: 3, MaxBattery: 20, StepCost: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Cars = append(w.Cars, van)
	w.Ledger.Money = 100

	for range 10 {
		w.AdvanceNoPause()
	}
	if len(w.Ledger.Cash) != 2 {
		t.Fatalf("cash entries = %d, want one per car: %+v", len(w.Ledger.Cash), w.Ledger.Cash)
	}
	if got := w.Ledger.Cash[0]; got.CarID != scooter.ID || got.Amount != -1 {
		t.Errorf("first row = %+v, want scooter -1", got)
	}
	if got := w.Ledger.Cash[1]; got.CarID != van.ID || got.Amount != -3 {
		t.Errorf("second row = %+v, want van -3", got)
	}
	checkCashChain(t, w)
}

// TestCashLedgerTowRecordsTheChargedNotTheBilledAmount is the clamp rule at the
// statement level: a $25+ tow against $5 moves $5 and says so, so the balance
// column still adds up.
func TestCashLedgerTowRecordsTheChargedNotTheBilledAmount(t *testing.T) {
	w := fleetWorld(t, 10)
	car := w.Cars[0]
	car.Pos = Position{5, 5}
	w.Ledger.Money = 5
	fee := w.TowFee(car, Position{1, 1})
	if fee <= 5 {
		t.Fatalf("tow fee = %d, test needs a fee above the $5 balance", fee)
	}

	if err := w.Tow(car.ID); err != nil {
		t.Fatalf("tow: %v", err)
	}
	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %d, want one tow row", len(w.Ledger.Cash))
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashTow || e.Amount != -5 || e.Balance != 0 {
		t.Errorf("tow entry = %+v, want -5 with balance 0", e)
	}
	if e.CarID != car.ID {
		t.Errorf("tow car = %q, want %q", e.CarID, car.ID)
	}
	if w.Ledger.TowFees != 5 {
		t.Errorf("towFees = %d, want 5", w.Ledger.TowFees)
	}
}

// TestCashLedgerExpiryFineIsTaggedToTheAssignedCar covers the other outflow
// that has a vehicle: a fine for an order a car was supposed to deliver.
func TestCashLedgerExpiryFineIsTaggedToTheAssignedCar(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]
	w.Ledger.Money = 100
	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	o.DeadlineTick = w.Tick - 1
	w.Advance()

	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %d, want the fine only: %+v", len(w.Ledger.Cash), w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashOrderFine || e.Amount != -o.Penalty {
		t.Errorf("fine entry = %+v, want %s of -%d", e, CashOrderFine, o.Penalty)
	}
	if e.CarID != car.ID || e.OrderID != o.ID {
		t.Errorf("fine tags = car %q order %q, want %q/%q", e.CarID, e.OrderID, car.ID, o.ID)
	}
	checkCashChain(t, w)
}

// TestCashLedgerCarPurchaseIsTagged checks the purchase entry names the car it
// bought, so the statement can attribute fleet spend.
func TestCashLedgerCarPurchaseIsTagged(t *testing.T) {
	w := tinyWorld(t)
	w.carClasses["van"] = CarClassConfig{
		Capacity: 3, MaxBattery: 20, StepCost: 1, Price: 400,
	}
	w.Ledger.Money = 1000

	van, err := w.BuyCar("van", "d1")
	if err != nil {
		t.Fatalf("buy car: %v", err)
	}
	buy := w.Ledger.Cash[len(w.Ledger.Cash)-1]
	if buy.Kind != CashCarPurchase || buy.Amount != -400 || buy.Balance != 600 {
		t.Errorf("purchase entry = %+v, want -400 with balance 600", buy)
	}
	if buy.CarID != van.ID || buy.CarName != van.Name {
		t.Errorf("purchase car = %q/%q, want %q/%q", buy.CarID, buy.CarName, van.ID, van.Name)
	}
	checkCashChain(t, w)
}

// TestCashLedgerGrowthGrantsAreFleetWide covers scheduled money grants: they
// enter the balance through the same door and are not attributed to a car.
func TestCashLedgerGrowthGrantsAreFleetWide(t *testing.T) {
	w := tinyWorld(t)
	w.applyGrowthGrants(GrowthConfig{
		Title:   "Harvest",
		Effects: []GrowthEffect{{Type: EffectGrantMoney, Amount: 50}},
	})

	if len(w.Ledger.Cash) != 1 {
		t.Fatalf("cash entries = %d, want the grant: %+v", len(w.Ledger.Cash), w.Ledger.Cash)
	}
	e := w.Ledger.Cash[0]
	if e.Kind != CashGrant || e.Amount != 50 || e.Balance != 50 {
		t.Errorf("grant entry = %+v, want +50 with balance 50", e)
	}
	if e.CarID != "" || e.CarName != "" {
		t.Errorf("grant tagged to car %q/%q, want none", e.CarID, e.CarName)
	}
	checkCashChain(t, w)
}

// TestCashLedgerOpeningEntry documents the starting balance as the first line
// of the statement, so a map that starts with money does not begin mid-account.
func TestCashLedgerOpeningEntry(t *testing.T) {
	l := NewLedger(500)
	if len(l.Cash) != 1 {
		t.Fatalf("cash entries = %d, want the opening line", len(l.Cash))
	}
	e := l.Cash[0]
	if e.Kind != CashOpening || e.Amount != 500 || e.Balance != 500 {
		t.Errorf("opening entry = %+v, want +500 at balance 500", e)
	}
	if l.NextCashSeq != 1 {
		t.Errorf("nextCashSeq = %d, want 1 so the first movement gets a fresh id", l.NextCashSeq)
	}
}

// TestCashLedgerSurvivesSaveRoundTrip is the persistence contract: a restored
// session keeps its statement, including seqs, tags and running balances, and
// keeps allocating new seqs after the ones already stored.
func TestCashLedgerSurvivesSaveRoundTrip(t *testing.T) {
	w := orderWorld(t)
	w.Advance()
	o := firstOrder(t, w)
	car := w.Cars[0]
	if err := w.AssignOrder(o.ID, car.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	driveTo(t, w, car, Position{2, 1})
	driveTo(t, w, car, Position{2, 3})

	saved := w.ExportState()
	// Export is a deep copy: mutating it must not rewrite the live statement.
	if len(saved.Ledger.Cash) != 1 {
		t.Fatalf("saved cash entries = %d, want 1", len(saved.Ledger.Cash))
	}
	saved.Ledger.Cash[0].Amount = 999
	if w.Ledger.Cash[0].Amount == 999 {
		t.Fatal("ExportState aliased the live cash slice")
	}
	saved.Ledger.Cash[0].Amount = w.Ledger.Cash[0].Amount

	restored := orderWorld(t)
	if err := restored.ImportState(saved); err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(restored.Ledger.Cash) != 1 {
		t.Fatalf("restored cash entries = %d, want 1", len(restored.Ledger.Cash))
	}
	got, want := restored.Ledger.Cash[0], w.Ledger.Cash[0]
	if got != want {
		t.Errorf("restored entry = %+v, want %+v", got, want)
	}
	if restored.Ledger.NextCashSeq != w.Ledger.NextCashSeq {
		t.Errorf("nextCashSeq = %d, want %d", restored.Ledger.NextCashSeq, w.Ledger.NextCashSeq)
	}
}

// TestSellCarBanksSeventyPercentAndRemovesTheCar pins the resale rule and the
// statement line: a sale is income tagged to the car, not a purchase refund.
func TestSellCarBanksSeventyPercentAndRemovesTheCar(t *testing.T) {
	w := tinyWorld(t)
	w.carClasses["van"] = CarClassConfig{
		Capacity: 3, MaxBattery: 20, StepCost: 1, Price: 400,
	}
	w.Ledger.Money = 1000
	van, err := w.BuyCar("van", "d1")
	if err != nil {
		t.Fatalf("buy car: %v", err)
	}
	if van.PurchasePrice != 400 {
		t.Fatalf("purchase price = %d, want 400", van.PurchasePrice)
	}
	if got := van.SaleValue(); got != 280 {
		t.Fatalf("sale value = %d, want 70%% of 400", got)
	}

	sold, refund, err := w.SellCar(van.ID)
	if err != nil {
		t.Fatalf("sell car: %v", err)
	}
	if sold != van || refund != 280 {
		t.Fatalf("sold %v for $%d, want the van for $280", sold, refund)
	}
	if _, ok := w.Car(van.ID); ok {
		t.Fatal("sold car is still in the fleet")
	}
	if w.Ledger.Money != 880 {
		t.Fatalf("money = %d, want 1000 - 400 + 280", w.Ledger.Money)
	}
	sale := w.Ledger.Cash[len(w.Ledger.Cash)-1]
	if sale.Kind != CashCarSale || sale.Amount != 280 || sale.CarID != van.ID {
		t.Fatalf("sale entry = %+v, want +280 tagged to %s", sale, van.ID)
	}
	if w.Ledger.MoneyEarned != 0 {
		t.Fatalf("moneyEarned = %d, want sale proceeds excluded from lifetime earnings", w.Ledger.MoneyEarned)
	}
	if last := w.Events[len(w.Events)-1]; last.Type != EventSoldCar {
		t.Fatalf("last event = %q, want %q", last.Type, EventSoldCar)
	}
	checkCashChain(t, w)
}

// TestSellCarSettlesChargingDebtFirst stops a charge-then-sell loop from
// erasing the bill.
func TestSellCarSettlesChargingDebtFirst(t *testing.T) {
	w := tinyWorld(t)
	addFreeCar(t, w)
	car := w.Cars[0]
	car.PurchasePrice = 100
	car.ChargeDue = 50
	car.ChargeUnits = 10
	car.ChargeSource = "ch1"
	w.Ledger.Money = 100

	if _, _, err := w.SellCar(car.ID); err != nil {
		t.Fatalf("sell car: %v", err)
	}
	if w.Ledger.Money != 120 {
		t.Fatalf("money = %d, want 100 - 50 + 70", w.Ledger.Money)
	}
	kinds := make([]CashEntryKind, 0, len(w.Ledger.Cash))
	for _, e := range w.Ledger.Cash {
		kinds = append(kinds, e.Kind)
	}
	want := []CashEntryKind{CashCharging, CashCarSale}
	if len(kinds) != len(want) || kinds[0] != want[0] || kinds[1] != want[1] {
		t.Fatalf("cash kinds = %v, want charging then sale", kinds)
	}
}

// TestSellCarRefusesAnOrderTheCarStillOwes keeps orders from being orphaned.
func TestSellCarRefusesAnOrderTheCarStillOwes(t *testing.T) {
	w := tinyWorld(t)
	addFreeCar(t, w)
	car := w.Cars[0]

	car.Cargo = []string{"o1"}
	if _, _, err := w.SellCar(car.ID); err == nil {
		t.Fatal("sold a car with cargo aboard")
	}
	car.Cargo = nil

	w.Orders = append(w.Orders, &Order{ID: "o1", State: OrderAssigned, AssignedCar: car.ID})
	if _, _, err := w.SellCar(car.ID); err == nil {
		t.Fatal("sold a car with an assigned order")
	}
	if _, ok := w.Car(car.ID); !ok {
		t.Fatal("refused sale still removed the car")
	}
}

// TestSellCarIgnoresDeliveredHistory proves DONE orders are history only: a
// car that finished its trips must still be sellable.
func TestSellCarIgnoresDeliveredHistory(t *testing.T) {
	w := tinyWorld(t)
	addFreeCar(t, w)
	car := w.Cars[0]
	w.Orders = append(w.Orders, &Order{ID: "o1", State: OrderDone, AssignedCar: car.ID})
	if _, _, err := w.SellCar(car.ID); err != nil {
		t.Fatalf("sell car after delivered order: %v", err)
	}
	if _, ok := w.Car(car.ID); ok {
		t.Fatal("sold car is still in the fleet")
	}
}

// TestExpiredOrderFreesTheCarForSale pins the whole expiry→sale path: an
// expired order is terminal, so both of SellCar's guards lapse. A car that only
// had the order assigned becomes sellable once the deadline passes, and a car
// that had already picked it up has its cargo dropped by expireOrders, so it is
// sellable too. Nothing else about the car changes: it keeps its position and
// battery and is IDLE again.
func TestExpiredOrderFreesTheCarForSale(t *testing.T) {
	t.Run("assigned but never picked up", func(t *testing.T) {
		w := orderWorld(t)
		w.Advance()
		o := firstOrder(t, w)
		car := w.Cars[0]
		if err := w.AssignOrder(o.ID, car.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := w.BuyCar("scooter", "d1"); err != nil {
			t.Fatalf("buy second car: %v", err)
		}
		if _, _, err := w.SellCar(car.ID); err == nil {
			t.Fatal("sold a car with a live assigned order")
		}

		w.Tick = o.DeadlineTick + 1
		w.Advance()

		if o.State != OrderExpired {
			t.Fatalf("state = %s, want expired", o.State)
		}
		if len(car.Cargo) != 0 {
			t.Fatalf("cargo = %v, want empty", car.Cargo)
		}
		if got := car.State(w.City); got != CarIdle {
			t.Fatalf("car state = %s, want idle after expiry", got)
		}
		if _, _, err := w.SellCar(car.ID); err != nil {
			t.Fatalf("sell after expiry: %v", err)
		}
	})

	t.Run("picked up then expired", func(t *testing.T) {
		w := orderWorld(t)
		w.Advance()
		o := firstOrder(t, w)
		car := w.Cars[0]
		if err := w.AssignOrder(o.ID, car.ID); err != nil {
			t.Fatal(err)
		}
		driveTo(t, w, car, Position{2, 1})
		if o.State != OrderPickedUp {
			t.Fatalf("state = %s, want picked_up", o.State)
		}
		x, y := car.Pos.X, car.Pos.Y
		battery := car.Battery
		if _, err := w.BuyCar("scooter", "d1"); err != nil {
			t.Fatalf("buy second car: %v", err)
		}
		if _, _, err := w.SellCar(car.ID); err == nil {
			t.Fatal("sold a car carrying an order")
		}

		w.Tick = o.DeadlineTick + 1
		w.Advance()

		if o.State != OrderExpired {
			t.Fatalf("state = %s, want expired", o.State)
		}
		if len(car.Cargo) != 0 || car.Load() != 0 {
			t.Fatalf("cargo = %v load = %d, want the expired boxes dropped", car.Cargo, car.Load())
		}
		if car.Pos.X != x || car.Pos.Y != y {
			t.Errorf("position moved to (%d,%d), want (%d,%d)", car.Pos.X, car.Pos.Y, x, y)
		}
		if car.Battery != battery {
			t.Errorf("battery = %d, want %d unchanged", car.Battery, battery)
		}
		if got := car.State(w.City); got != CarIdle {
			t.Fatalf("car state = %s, want idle after expiry", got)
		}
		if _, _, err := w.SellCar(car.ID); err != nil {
			t.Fatalf("sell after expiry: %v", err)
		}
	})
}

// addFreeCar gives a test world a second vehicle so a sale can leave one
// behind; the engine refuses to sell the last car.
func addFreeCar(t *testing.T, w *World) {
	t.Helper()
	if _, err := w.BuyCar("scooter", "d1"); err != nil {
		t.Fatalf("buy second scooter: %v", err)
	}
}

// TestSellCarKeepsTheLastVehicle avoids a fleet-less save with no way to earn.
func TestSellCarKeepsTheLastVehicle(t *testing.T) {
	w := tinyWorld(t)
	if _, _, err := w.SellCar(w.Cars[0].ID); err == nil {
		t.Fatal("sold the only vehicle")
	}
	if len(w.Cars) != 1 {
		t.Fatalf("cars = %d, want the last vehicle kept", len(w.Cars))
	}
}

// TestStarterCarHasNoResaleValue: the free starter scooter must not be worth
// 70% of its class list price, because the player never paid that price.
func TestStarterCarHasNoResaleValue(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	if car.PaidPrice() != 0 || car.SaleValue() != 0 {
		t.Fatalf("starter car paid %d sale %d, want 0/0", car.PaidPrice(), car.SaleValue())
	}
}

// TestImportBackfillsLegacyPurchasePrice recovers a pre-field save's paid price
// from its own cash ledger, so old bought cars keep the 70% rule while a free
// starter still has no resale value.
func TestImportBackfillsLegacyPurchasePrice(t *testing.T) {
	w := tinyWorld(t)
	car := w.Cars[0]
	car.PurchasePrice = 0 // what an old save decodes as
	w.Ledger.Cash = append(w.Ledger.Cash, CashEntry{
		Seq: 1, Kind: CashCarPurchase, Amount: -400, CarID: car.ID, Balance: 100,
	})
	saved := w.ExportState()

	restored := tinyWorld(t)
	if err := restored.ImportState(saved); err != nil {
		t.Fatal(err)
	}
	got := restored.Cars[0]
	if got.PurchasePrice != 400 || got.SaleValue() != 280 {
		t.Fatalf("restored paid %d sale %d, want 400/280", got.PurchasePrice, got.SaleValue())
	}
}

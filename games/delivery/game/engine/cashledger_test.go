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

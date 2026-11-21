package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCashMonthsGroupKindsByCategory pins the report contract: every movement
// lands in exactly one month, income and expenses are positive magnitudes, and
// opening capital is not monthly income.
func TestCashMonthsGroupKindsByCategory(t *testing.T) {
	w := tinyWorld(t)
	w.Ledger = NewLedger(1000)
	car := w.Cars[0]

	w.Tick = 0
	w.recordCash(CashDelivery, 100, car, "o1", "delivery")
	w.recordCash(CashLateDelivery, 50, car, "o2", "late delivery")
	w.recordCash(CashGrant, 25, nil, "", "grant")
	w.recordCash(CashOrderFine, -20, car, "o3", "fine")
	w.recordCash(CashUpkeep, -5, car, "", "upkeep")
	w.recordCash(CashCharging, -3, car, "", "charging")
	w.recordCash(CashTow, -25, car, "", "tow")
	w.recordCash(CashCarPurchase, -100, car, "", "car")

	w.Tick = 599
	w.recordCash(CashDelivery, 10, car, "o4", "last tick of month one")
	w.Tick = 600
	w.recordCash(CashDelivery, 70, car, "o5", "first tick of month two")

	if len(w.Ledger.Months) != 2 {
		t.Fatalf("months = %d, want 2: %+v", len(w.Ledger.Months), w.Ledger.Months)
	}
	m1, m2 := w.Ledger.Months[0], w.Ledger.Months[1]
	if m1.Index != 1 || m1.StartTick != 0 || m1.EndTick != 599 {
		t.Fatalf("month one bounds = %+v", m1)
	}
	if m1.Income != 185 || m1.Expenses != 153 {
		t.Fatalf("month one = income %d expenses %d, want 185/153", m1.Income, m1.Expenses)
	}
	if m2.Index != 2 || m2.StartTick != 600 || m2.EndTick != 1199 || m2.Income != 70 || m2.Expenses != 0 {
		t.Fatalf("month two = %+v", m2)
	}

	want := []MonthCategory{
		{Category: CashCategoryDeliveryIncome, Income: 160},
		{Category: CashCategoryRewards, Income: 25},
		{Category: CashCategoryOrderFines, Expenses: 20},
		{Category: CashCategoryUpkeep, Expenses: 5},
		{Category: CashCategoryCharging, Expenses: 3},
		{Category: CashCategoryTows, Expenses: 25},
		{Category: CashCategoryCarPurchases, Expenses: 100},
	}
	if len(m1.Categories) != len(want) {
		t.Fatalf("month one categories = %+v, want %+v", m1.Categories, want)
	}
	for i := range want {
		if m1.Categories[i] != want[i] {
			t.Errorf("category %d = %+v, want %+v", i, m1.Categories[i], want[i])
		}
	}
	if len(m2.Categories) != 1 || m2.Categories[0] != (MonthCategory{Category: CashCategoryDeliveryIncome, Income: 70}) {
		t.Fatalf("month two categories = %+v", m2.Categories)
	}
	for _, c := range m1.Categories {
		if c.Category == CashCategoryOpening {
			t.Fatal("opening capital leaked into monthly income")
		}
	}
}

// TestCashMonthsSurviveSaveAsDerivedState proves the summaries are not a
// second source of truth: the save omits them and ImportState rebuilds them
// from the statement.
func TestCashMonthsSurviveSaveAsDerivedState(t *testing.T) {
	w := tinyWorld(t)
	w.Ledger = NewLedger(100)
	w.Tick = 600
	w.recordCash(CashDelivery, 40, w.Cars[0], "o1", "delivery")

	raw, err := json.Marshal(w.ExportState())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Months") {
		t.Fatal("monthly summaries should be derived and excluded from the save")
	}

	var saved SavedState
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Ledger != nil {
		saved.Ledger.Months = nil
	}
	restored := tinyWorld(t)
	if err := restored.ImportState(saved); err != nil {
		t.Fatal(err)
	}
	if len(restored.Ledger.Months) != 2 {
		t.Fatalf("restored months = %+v, want the zero month 1 and month 2", restored.Ledger.Months)
	}
	m2 := restored.Ledger.Months[1]
	if m2.Income != 40 || m2.Expenses != 0 || len(m2.Categories) != 1 {
		t.Fatalf("restored month two = %+v", m2)
	}
}

// TestEveryCashKindHasCategory guards the monthly report against a future
// kind silently falling outside the buckets. Other is a valid answer, but the
// mapping must be deliberate.
func TestEveryCashKindHasCategory(t *testing.T) {
	for _, k := range []CashEntryKind{
		CashOpening, CashDelivery, CashLateDelivery, CashOrderFine,
		CashUpkeep, CashCharging, CashTow, CashCarPurchase, CashGrant,
	} {
		if got := k.Category(); got == "" {
			t.Errorf("kind %s has no category", k)
		}
	}
}

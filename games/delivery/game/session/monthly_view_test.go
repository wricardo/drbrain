package session

import (
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// TestMonthsViewFillsQuietMonthsAndBounds pins the client shape: newest first,
// capped at maxMonths, and a month with no entries is still present with a
// zero summary so month numbers never skip.
func TestMonthsViewFillsQuietMonthsAndBounds(t *testing.T) {
	summaries := []engine.MonthSummary{
		{
			Index: 4, StartTick: 1800, EndTick: 2399, Income: 100, Expenses: 40,
			Categories: []engine.MonthCategory{
				{Category: engine.CashCategoryDeliveryIncome, Income: 100},
				{Category: engine.CashCategoryCharging, Expenses: 40},
			},
		},
		{
			Index: 14, StartTick: 7800, EndTick: 8399, Income: 70,
			Categories: []engine.MonthCategory{
				{Category: engine.CashCategoryDeliveryIncome, Income: 70},
			},
		},
	}

	got := monthsView(summaries, 14, 600, maxMonths)
	if len(got) != maxMonths {
		t.Fatalf("months = %d, want %d", len(got), maxMonths)
	}
	if got[0].Index != 14 || got[len(got)-1].Index != 3 {
		t.Fatalf("window = %d..%d, want 14..3", got[0].Index, got[len(got)-1].Index)
	}
	if got[0].Net != 70 || got[0].Categories[0].Category != "delivery_income" {
		t.Fatalf("newest month = %+v", got[0])
	}

	var month4 *MonthView
	for i := range got {
		if got[i].Index == 4 {
			month4 = &got[i]
			break
		}
	}
	if month4 == nil {
		t.Fatal("month 4 missing from the window")
	}
	if month4.Income != 100 || month4.Expenses != 40 || month4.Net != 60 {
		t.Fatalf("month 4 totals = %+v", month4)
	}
	if len(month4.Categories) != 2 ||
		month4.Categories[0].Category != "delivery_income" ||
		month4.Categories[1].Category != "charging" {
		t.Fatalf("month 4 categories = %+v", month4.Categories)
	}

	var quiet *MonthView
	for i := range got {
		if got[i].Index == 5 {
			quiet = &got[i]
			break
		}
	}
	if quiet == nil {
		t.Fatal("quiet month 5 missing")
	}
	if quiet.Income != 0 || quiet.Expenses != 0 || quiet.Net != 0 {
		t.Fatalf("quiet month totals = %+v", quiet)
	}
	if quiet.Categories == nil {
		t.Fatal("quiet month categories must be [] not null")
	}
	if quiet.StartTick != 2400 || quiet.EndTick != 2999 {
		t.Fatalf("quiet month bounds = %+v", quiet)
	}
}

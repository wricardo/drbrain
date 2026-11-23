package session

import (
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// TestMonthsViewFillsQuietMonthsAndBounds pins the client shape: newest first,
// bounded to the requested window, and a month with no entries is still present
// with a zero summary so month numbers never skip.
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

	got := monthRange(summaries, 12, 14, 600, "")
	if len(got) != 3 {
		t.Fatalf("months = %d, want 3", len(got))
	}
	if got[0].Index != 14 || got[len(got)-1].Index != 12 {
		t.Fatalf("window = %d..%d, want 14..12", got[0].Index, got[len(got)-1].Index)
	}
	if got[0].Net != 70 || got[0].Categories[0].Category != "delivery_income" {
		t.Fatalf("newest month = %+v", got[0])
	}

	// Month 13 has no summary: it must still be a row, with the bounds its
	// index implies and an empty category list rather than nil.
	quiet := got[1]
	if quiet.Index != 13 {
		t.Fatalf("middle month = %d, want 13", quiet.Index)
	}
	if quiet.Income != 0 || quiet.Expenses != 0 || quiet.Net != 0 {
		t.Fatalf("quiet month totals = %+v", quiet)
	}
	if quiet.Categories == nil {
		t.Fatal("quiet month categories must be [] not null")
	}
	if quiet.StartTick != 7200 || quiet.EndTick != 7799 {
		t.Fatalf("quiet month bounds = %+v", quiet)
	}

	// A wider window still renders the older month with its own totals.
	wide := monthRange(summaries, 4, 4, 600, "")
	if len(wide) != 1 || wide[0].Income != 100 || wide[0].Expenses != 40 || wide[0].Net != 60 {
		t.Fatalf("month 4 = %+v", wide)
	}
	if len(wide[0].Categories) != 2 ||
		wide[0].Categories[0].Category != "delivery_income" ||
		wide[0].Categories[1].Category != "charging" {
		t.Fatalf("month 4 categories = %+v", wide[0].Categories)
	}
}

// TestMonthRangeFiltersCategory proves a category-filtered month keeps only
// that bucket and recomputes its totals, rather than reporting the whole-month
// total next to a filtered category list.
func TestMonthRangeFiltersCategory(t *testing.T) {
	summaries := []engine.MonthSummary{{
		Index: 7, StartTick: 3600, EndTick: 4199, Income: 1000, Expenses: 300,
		Categories: []engine.MonthCategory{
			{Category: engine.CashCategoryDeliveryIncome, Income: 1000},
			{Category: engine.CashCategoryTows, Expenses: 300},
		},
	}}

	got := monthRange(summaries, 7, 7, 600, "tows")
	if len(got) != 1 {
		t.Fatalf("months = %d, want 1", len(got))
	}
	m := got[0]
	if len(m.Categories) != 1 || m.Categories[0].Category != "tows" {
		t.Fatalf("categories = %+v", m.Categories)
	}
	if m.Income != 0 || m.Expenses != 300 || m.Net != -300 {
		t.Fatalf("filtered totals = %+v", m)
	}
}

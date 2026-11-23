package session

import (
	"errors"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// seedStatement installs a statement the tests can filter: one entry at the
// start of each of four months, with a second delivery and a tow inside month
// two so car and category filters have something to reject. It runs through Do,
// so it takes the same lock a resolver would.
func seedStatement(t *testing.T, s *Session) {
	t.Helper()
	s.Do(func(w *engine.World) {
		w.Tick = 2100 // month 4, tick 2100 of ticks 0..599 per month
		w.Ledger.Cash = []engine.CashEntry{
			{Seq: 1, Tick: 10, Kind: engine.CashOpening, Amount: 500, Balance: 500, Memo: "opening"},
			{Seq: 2, Tick: 100, Kind: engine.CashDelivery, Amount: 100, Balance: 600, CarID: "car_1", Memo: "month one"},
			{Seq: 3, Tick: 700, Kind: engine.CashDelivery, Amount: 40, Balance: 640, CarID: "car_1", Memo: "month two a"},
			{Seq: 4, Tick: 900, Kind: engine.CashTow, Amount: -60, Balance: 580, CarID: "car_2", Memo: "month two tow"},
			{Seq: 5, Tick: 1300, Kind: engine.CashUpkeep, Amount: -20, Balance: 560, Memo: "month three"},
			{Seq: 6, Tick: 1900, Kind: engine.CashDelivery, Amount: 70, Balance: 630, CarID: "car_2", Memo: "month four"},
		}
	})
}

// mustCash reads the statement and fails the test on an unexpected error.
func mustCash(t *testing.T, s *Session, filter StatementFilter) []CashEntryView {
	t.Helper()
	rows, err := s.StatementCashEntries(filter)
	if err != nil {
		t.Fatalf("statement cash entries: %v", err)
	}
	return rows
}

// mustMonths reads the monthly report and fails on an unexpected error.
func mustMonths(t *testing.T, s *Session, filter StatementFilter) []MonthView {
	t.Helper()
	rows, err := s.StatementMonths(filter)
	if err != nil {
		t.Fatalf("statement months: %v", err)
	}
	return rows
}

// TestStatementFutureMonthIsAnError proves a month that has not started is
// rejected rather than reported as an empty, quiet month.
func TestStatementFutureMonthIsAnError(t *testing.T) {
	s := newTestSession(t)
	seedStatement(t, s) // tick 2100 = month 4

	month := 5
	if _, err := s.StatementMonths(StatementFilter{Month: &month}); err == nil {
		t.Fatal("future month must error")
	} else {
		var notStarted *ErrMonthNotStarted
		if !errors.As(err, &notStarted) || notStarted.Current != 4 {
			t.Fatalf("error = %v, want ErrMonthNotStarted for current month 4", err)
		}
	}
	if _, err := s.StatementCashEntries(StatementFilter{Month: &month}); err == nil {
		t.Fatal("future month must error for the statement too")
	}
	if _, err := s.StatementCars(StatementFilter{Month: &month}); err == nil {
		t.Fatal("future month must error for the per-car aggregate")
	}

	// The current month is still readable, including its partial totals.
	current := 4
	if rows := mustMonths(t, s, StatementFilter{Month: &current}); len(rows) != 1 {
		t.Fatalf("current month rows = %+v, want one", rows)
	}
}

// TestStatementCarsAggregates proves the per-car totals: only car-tagged rows
// count, totals are per car, and the highest row count comes first.
func TestStatementCarsAggregates(t *testing.T) {
	s := newTestSession(t)
	seedStatement(t, s)

	rows, err := s.StatementCars(StatementFilter{})
	if err != nil {
		t.Fatalf("statement cars: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("cars = %+v, want two", rows)
	}
	// car_1 has two rows (100 in, 40 in), car_2 has two (60 out, 70 in);
	// the tie breaks on id, and world rows (opening, upkeep) appear in neither.
	for _, r := range rows {
		switch r.CarID {
		case "car_1":
			if r.Income != 140 || r.Expenses != 0 || r.Net != 140 || r.Rows != 2 {
				t.Fatalf("car_1 = %+v", r)
			}
		case "car_2":
			if r.Income != 70 || r.Expenses != 60 || r.Net != 10 || r.Rows != 2 {
				t.Fatalf("car_2 = %+v", r)
			}
		default:
			t.Fatalf("unexpected car row %+v", r)
		}
	}

	month2 := 2
	rows, err = s.StatementCars(StatementFilter{Month: &month2})
	if err != nil {
		t.Fatalf("month 2 cars: %v", err)
	}
	if len(rows) != 2 || rows[0].CarID != "car_1" || rows[0].Income != 40 {
		t.Fatalf("month 2 cars = %+v", rows)
	}
}

// TestStatementCashEntriesFilters covers every filter the resolver exposes over
// a real statement: month, tick window, category, car and limit, all newest
// first.
func TestStatementCashEntriesFilters(t *testing.T) {
	s := newTestSession(t)
	seedStatement(t, s)

	all := mustCash(t, s, StatementFilter{})
	if len(all) != 6 {
		t.Fatalf("unfiltered rows = %d, want 6", len(all))
	}
	if all[0].Seq != 6 || all[len(all)-1].Seq != 1 {
		t.Fatalf("order = %d..%d, want newest first 6..1", all[0].Seq, all[len(all)-1].Seq)
	}

	month2 := 2
	got := mustCash(t, s, StatementFilter{Month: &month2})
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 3 {
		t.Fatalf("month 2 = %+v, want seq 4,3", got)
	}

	from, to := 650, 1350
	got = mustCash(t, s, StatementFilter{FromTick: &from, ToTick: &to})
	if len(got) != 3 || got[0].Seq != 5 || got[2].Seq != 3 {
		t.Fatalf("tick window = %+v, want seq 5,4,3", got)
	}

	got = mustCash(t, s, StatementFilter{Category: "tows"})
	if len(got) != 1 || got[0].Seq != 4 {
		t.Fatalf("category tows = %+v, want seq 4", got)
	}

	car := "car_2"
	got = mustCash(t, s, StatementFilter{CarID: car})
	if len(got) != 2 || got[0].Seq != 6 || got[1].Seq != 4 {
		t.Fatalf("carId car_2 = %+v, want seq 6,4", got)
	}

	limit := 2
	got = mustCash(t, s, StatementFilter{Limit: limit})
	if len(got) != 2 || got[0].Seq != 6 || got[1].Seq != 5 {
		t.Fatalf("limit 2 = %+v, want seq 6,5", got)
	}

	// A window past the current tick is empty rather than clamped into the
	// present, so a caller can tell "nothing then" from "the latest rows".
	future := 9000
	if rows := mustCash(t, s, StatementFilter{FromTick: &future}); len(rows) != 0 {
		t.Fatalf("future window = %+v, want empty", rows)
	}
}

// TestStatementMonthsFilters proves the month window selects whole months and
// that a category filter recomputes the row from that bucket only.
func TestStatementMonthsFilters(t *testing.T) {
	s := newTestSession(t)
	s.Do(func(w *engine.World) {
		w.Tick = 2100
		w.Ledger.Months = []engine.MonthSummary{
			{Index: 1, StartTick: 0, EndTick: 599, Income: 100,
				Categories: []engine.MonthCategory{{Category: engine.CashCategoryDeliveryIncome, Income: 100}}},
			{Index: 2, StartTick: 600, EndTick: 1199, Income: 40, Expenses: 60,
				Categories: []engine.MonthCategory{
					{Category: engine.CashCategoryDeliveryIncome, Income: 40},
					{Category: engine.CashCategoryTows, Expenses: 60},
				}},
			{Index: 4, StartTick: 1800, EndTick: 2399, Income: 70,
				Categories: []engine.MonthCategory{{Category: engine.CashCategoryDeliveryIncome, Income: 70}}},
		}
	})

	// No filter defaults to the newest three months: 4, 3, 2 — with month 3
	// zero-filled because the engine kept no summary for it.
	got := mustMonths(t, s, StatementFilter{})
	if len(got) != DefaultStatementMonths {
		t.Fatalf("default months = %d, want %d", len(got), DefaultStatementMonths)
	}
	if got[0].Index != 4 || got[1].Index != 3 || got[2].Index != 2 {
		t.Fatalf("default window = %d,%d,%d, want 4,3,2", got[0].Index, got[1].Index, got[2].Index)
	}
	if got[1].Income != 0 || got[1].Categories == nil {
		t.Fatalf("quiet month = %+v, want zero row with [] categories", got[1])
	}

	month2 := 2
	got = mustMonths(t, s, StatementFilter{Month: &month2})
	if len(got) != 1 || got[0].Income != 40 || got[0].Expenses != 60 || got[0].Net != -20 {
		t.Fatalf("month 2 = %+v", got)
	}

	got = mustMonths(t, s, StatementFilter{Month: &month2, Category: "tows"})
	if len(got) != 1 || len(got[0].Categories) != 1 || got[0].Categories[0].Category != "tows" {
		t.Fatalf("month 2 tows = %+v", got)
	}
	if got[0].Income != 0 || got[0].Expenses != 60 || got[0].Net != -60 {
		t.Fatalf("month 2 tows totals = %+v", got[0])
	}

	// A tick range selects every month it intersects, oldest month included.
	from := 100
	got = mustMonths(t, s, StatementFilter{FromTick: &from, Limit: 10})
	if len(got) != 4 || got[0].Index != 4 || got[3].Index != 1 {
		t.Fatalf("tick window months = %+v", got)
	}
}

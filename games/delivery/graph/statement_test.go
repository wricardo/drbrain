package graph

import (
	"errors"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// fakeReader records the lowered filter and returns canned rows. It exercises
// the statementSource translation without a session or a database.
type fakeReader struct {
	got    session.StatementFilter
	months []session.MonthView
	cash   []session.CashEntryView
	cars   []session.CarLedgerView
	err    error
}

func (f *fakeReader) StatementMonths(filter session.StatementFilter) ([]session.MonthView, error) {
	f.got = filter
	return f.months, f.err
}

func (f *fakeReader) StatementCashEntries(filter session.StatementFilter) ([]session.CashEntryView, error) {
	f.got = filter
	return f.cash, f.err
}

func (f *fakeReader) StatementCars(filter session.StatementFilter) ([]session.CarLedgerView, error) {
	f.got = filter
	return f.cars, f.err
}

// TestStatementSourceLowersFilter pins the model->session translation: the
// GraphQL enum must reach the engine as the lowercase category the ledger uses,
// or every filtered read silently matches nothing.
func TestStatementSourceLowersFilter(t *testing.T) {
	month, from, to, limit := 3, 10, 20, 7
	category := model.CashCategoryTows
	car := "car_1"
	filter := model.StatementFilter{
		Month: &month, FromTick: &from, ToTick: &to, Category: &category, CarID: &car, Limit: limit,
	}

	fake := &fakeReader{}
	if _, err := (statementSource{reader: fake}).CashEntries(filter); err != nil {
		t.Fatalf("cash entries: %v", err)
	}
	got := fake.got
	if got.Category != "tows" {
		t.Errorf("category = %q, want the lowercase engine name", got.Category)
	}
	if got.CarID != car || got.Month == nil || *got.Month != month ||
		got.FromTick == nil || *got.FromTick != from || got.ToTick == nil || *got.ToTick != to ||
		got.Limit != limit {
		t.Errorf("filter forwarded = %+v", got)
	}

	// No category or car means "every row": the conversion must not invent a
	// filter when the argument is nil.
	fake = &fakeReader{}
	if _, err := (statementSource{reader: fake}).Months(model.StatementFilter{}); err != nil {
		t.Fatalf("months: %v", err)
	}
	if fake.got.Category != "" || fake.got.CarID != "" {
		t.Errorf("empty filter widened = %+v", fake.got)
	}
}

// TestStatementSourceConvertsRows covers the lowercase engine rows -> uppercase
// GraphQL enums, and the optional car/order pointers a row may omit.
func TestStatementSourceConvertsRows(t *testing.T) {
	fake := &fakeReader{
		months: []session.MonthView{{
			Index: 3, StartTick: 1200, EndTick: 1799, Income: 50, Expenses: 20, Net: 30,
			Categories: []session.MonthCategoryView{{Category: "delivery_income", Income: 50}},
		}},
		cash: []session.CashEntryView{{
			Seq: 1, Tick: 12, Kind: "delivery", Category: "delivery_income", Amount: 40, Balance: 90,
			CarID: "car_1", CarName: "S1",
		}},
		cars: []session.CarLedgerView{{CarID: "car_1", CarName: "S1", Income: 40, Expenses: 5, Net: 35, Rows: 2}},
	}
	src := statementSource{reader: fake}

	months, err := src.Months(model.StatementFilter{})
	if err != nil {
		t.Fatalf("months: %v", err)
	}
	if len(months) != 1 || months[0].Index != 3 || months[0].StartTick != 1200 || months[0].EndTick != 1799 {
		t.Fatalf("months = %+v", months)
	}
	if len(months[0].Categories) != 1 || months[0].Categories[0].Category != model.CashCategoryDeliveryIncome {
		t.Fatalf("month categories = %+v", months[0].Categories)
	}

	rows, err := src.CashEntries(model.StatementFilter{})
	if err != nil {
		t.Fatalf("cash entries: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != model.CashKindDelivery || rows[0].Category != model.CashCategoryDeliveryIncome {
		t.Fatalf("cash row = %+v", rows[0])
	}
	if rows[0].CarID == nil || *rows[0].CarID != "car_1" || rows[0].CarName == nil || *rows[0].CarName != "S1" {
		t.Fatalf("car fields = %+v", rows[0])
	}
	if rows[0].OrderID != nil {
		t.Errorf("order id = %v, want nil for a row with no order", rows[0].OrderID)
	}

	cars, err := src.Cars(model.StatementFilter{})
	if err != nil {
		t.Fatalf("cars: %v", err)
	}
	if len(cars) != 1 || cars[0].CarID != "car_1" || cars[0].Income != 40 || cars[0].Rows != 2 {
		t.Fatalf("car ledger = %+v", cars)
	}
}

// TestStatementSourceMapsMonthNotStarted: asking for a future month is a bad
// request, not an empty page, and any other reader error must survive so its
// own code mapping still works.
func TestStatementSourceMapsMonthNotStarted(t *testing.T) {
	future := &fakeReader{err: &session.ErrMonthNotStarted{Month: 9, Current: 3}}
	_, err := (statementSource{reader: future}).Months(model.StatementFilter{})
	if err == nil || !strings.HasPrefix(errMessage(err), "bad_request:") {
		t.Fatalf("error = %v, want a bad_request prefix", err)
	}

	sentinel := errors.New("reader exploded")
	plain := &fakeReader{err: sentinel}
	if _, err = (statementSource{reader: plain}).CashEntries(model.StatementFilter{}); !errors.Is(err, sentinel) {
		t.Fatalf("cash entries error = %v, want the reader's error unchanged", err)
	}
	if _, err = (statementSource{reader: plain}).Cars(model.StatementFilter{}); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the reader's error unchanged", err)
	}
}

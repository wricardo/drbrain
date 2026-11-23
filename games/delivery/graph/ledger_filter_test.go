package graph

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// errMessage returns the message a GraphQL client will see. The resolver's Go
// error also carries gqlgen's field context, so matching on err.Error() would
// test the wrapper rather than the contract.
func errMessage(err error) string {
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return gqlErr.Message
	}
	return err.Error()
}

// fakeStatement records the filter it was asked for, so the resolver contract
// (arguments reach the history reader, defaults stay untouched) is checked
// without a database.
type fakeStatement struct {
	got model.StatementFilter
}

func (f *fakeStatement) Months(filter model.StatementFilter) ([]*model.MonthView, error) {
	f.got = filter
	return []*model.MonthView{{Index: 9}}, nil
}

func (f *fakeStatement) CashEntries(filter model.StatementFilter) ([]*model.CashEntryView, error) {
	f.got = filter
	return []*model.CashEntryView{{Seq: 99, Tick: 10, Amount: -5}}, nil
}

func (f *fakeStatement) Cars(filter model.StatementFilter) ([]*model.CarLedgerView, error) {
	f.got = filter
	return []*model.CarLedgerView{{CarID: "car_1", Rows: 3}}, nil
}

// TestLedgerMonthsResolverDefaultAndFilter pins the two paths: no arguments
// returns the snapshot window without touching history, and a filtered read
// forwards the arguments to the bound statement source.
func TestLedgerMonthsResolverDefaultAndFilter(t *testing.T) {
	r := &ledgerViewResolver{}
	window := []*model.MonthView{{Index: 3}, {Index: 2}, {Index: 1}}
	obj := &model.LedgerView{Months: window, CashEntries: []*model.CashEntryView{{Seq: 1}}}

	got, err := r.Months(context.Background(), obj, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("default months: %v", err)
	}
	if len(got) != len(window) || got[0].Index != 3 {
		t.Fatalf("default months = %+v, want the snapshot window", got)
	}

	// Asking for history on a snapshot that carries none is a bad_request, not
	// a short page that looks like "no activity".
	month := 2
	if _, err := r.Months(context.Background(), obj, &month, nil, nil, nil, nil); err == nil {
		t.Fatal("filtered months on a window-only view must fail")
	} else if !strings.HasPrefix(errMessage(err), "bad_request:") {
		t.Fatalf("error = %v, want bad_request prefix", err)
	}

	cat := model.CashCategoryTows
	fake := &fakeStatement{}
	obj.SetStatement(fake)
	got, err = r.Months(context.Background(), obj, &month, nil, nil, &cat, nil)
	if err != nil {
		t.Fatalf("filtered months: %v", err)
	}
	if len(got) != 1 || got[0].Index != 9 {
		t.Fatalf("filtered months = %+v, want the source rows", got)
	}
	if fake.got.Month == nil || *fake.got.Month != 2 || fake.got.Category == nil || *fake.got.Category != cat {
		t.Fatalf("filter forwarded = %+v", fake.got)
	}
}

// TestLedgerCashEntriesResolverDefaultAndFilter mirrors the months contract and
// covers carId, which only the statement exposes.
func TestLedgerCashEntriesResolverDefaultAndFilter(t *testing.T) {
	r := &ledgerViewResolver{}
	window := []*model.CashEntryView{{Seq: 2}, {Seq: 1}}
	obj := &model.LedgerView{CashEntries: window}

	got, err := r.CashEntries(context.Background(), obj, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("default rows: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 2 {
		t.Fatalf("default rows = %+v, want the snapshot window", got)
	}

	fake := &fakeStatement{}
	obj.SetStatement(fake)
	car := "car_2"
	limit := 7
	got, err = r.CashEntries(context.Background(), obj, nil, nil, nil, nil, &car, &limit)
	if err != nil {
		t.Fatalf("filtered rows: %v", err)
	}
	if len(got) != 1 || got[0].Seq != 99 {
		t.Fatalf("filtered rows = %+v, want the source rows", got)
	}
	if fake.got.CarID == nil || *fake.got.CarID != car || fake.got.Limit != 7 {
		t.Fatalf("filter forwarded = %+v", fake.got)
	}
}

// TestLedgerStatementArgValidation pins the arguments that cannot describe a
// window, so a typo is an error instead of a silently empty page.
func TestLedgerStatementArgValidation(t *testing.T) {
	r := &ledgerViewResolver{}
	obj := &model.LedgerView{}
	obj.SetStatement(&fakeStatement{})

	zero, negative, past, future := 0, -1, 50, 10
	cases := []struct {
		name string
		call func() error
	}{
		{"month zero", func() error {
			_, err := r.Months(context.Background(), obj, &zero, nil, nil, nil, nil)
			return err
		}},
		{"negative from", func() error {
			_, err := r.Months(context.Background(), obj, nil, &negative, nil, nil, nil)
			return err
		}},
		{"negative to", func() error {
			_, err := r.Months(context.Background(), obj, nil, nil, &negative, nil, nil)
			return err
		}},
		{"window inverted", func() error {
			_, err := r.Months(context.Background(), obj, nil, &past, &future, nil, nil)
			return err
		}},
		{"zero limit", func() error {
			_, err := r.CashEntries(context.Background(), obj, nil, nil, nil, nil, nil, &zero)
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.call()
		if err == nil || !strings.HasPrefix(errMessage(err), "bad_request:") {
			t.Errorf("%s: error = %v, want bad_request prefix", tc.name, err)
		}
	}
}

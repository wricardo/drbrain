package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// CarLedgerView is one car's income, expenses and net over a statement window.
// Rows is how many statement rows the totals came from, so a caller can tell a
// car with one big delivery from one with fifty small ones.
type CarLedgerView struct {
	CarID    string
	CarName  string
	Income   int
	Expenses int
	Net      int
	Rows     int
}

// StatementFilter narrows a read over the accounting history. The zero value
// constrains nothing, so every field is optional:
//
//   - Month, when set, is the 1-based accounting month; it overrides the tick
//     window with that month's own bounds.
//   - FromTick and ToTick are inclusive tick bounds. They intersect with Month
//     when both are given.
//   - Category keeps one economic bucket, matched case-insensitively against
//     engine.CashCategory. Empty means every bucket.
//   - CarID keeps rows attributed to one car. Empty means every car, including
//     rows that belong to no car.
//   - Limit caps the rows returned, newest first. Zero means the default.
type StatementFilter struct {
	Month    *int
	FromTick *int
	ToTick   *int
	Category string
	CarID    string
	Limit    int
}

// StatementReader serves filtered reads over a session's complete accounting
// history.
//
// A snapshot carries only a bounded window of the statement (see
// DefaultStatementMonths), because a frame is re-sent on every tick. This
// reader exists so a client can ask for an older month or a tick range without
// rehydrating the session and without the snapshot carrying years of rows.
// BuildSnapshot never copies the ledger into it: reads take the session lock
// and walk the live ledger, so a filtered read costs nothing until it happens.
//
// A nil StatementReader is valid and means the view carries only its own
// bounded window.
type StatementReader interface {
	// StatementMonths returns month rows intersecting the filter window,
	// newest first. Month totals cover the whole accounting month even when
	// the filter window clips it: the window selects which months to include.
	// A month in the future is an error, not an empty page.
	StatementMonths(filter StatementFilter) ([]MonthView, error)
	// StatementCashEntries returns statement rows matching the filter, newest
	// first.
	StatementCashEntries(filter StatementFilter) ([]CashEntryView, error)
	// StatementCars aggregates the matching rows per car, highest row count
	// first. Rows that belong to no car are excluded.
	StatementCars(filter StatementFilter) ([]CarLedgerView, error)
}

// ErrMonthNotStarted reports a read for an accounting month that has not begun.
// It is a caller mistake, not empty data, so it must not look like "no activity".
type ErrMonthNotStarted struct {
	Month   int
	Current int
}

func (e *ErrMonthNotStarted) Error() string {
	return fmt.Sprintf("month %d has not started (current month %d)", e.Month, e.Current)
}

// Default rows for a filtered read. They mirror what a snapshot carries so an
// unfiltered request through the reader costs about the same as the prebuilt
// window.
const (
	// DefaultStatementMonths is how many months a snapshot carries and how
	// many an unfiltered months read returns.
	DefaultStatementMonths = 3
	// DefaultCashRows is how many statement rows an unfiltered read returns.
	DefaultCashRows = 200
	// MaxStatementRows caps a single statement read, so a client cannot ask
	// for a multi-megabyte frame by passing a huge limit.
	MaxStatementRows = 1000
)

// maxCashRows bounds the statement rows a snapshot frame carries. The engine
// keeps the full ledger for the save and the statement chain; a snapshot is
// re-sent on every mutation and every streamed tick, so carrying years of rows
// would make each keypress pay for the whole history. 200 rows covers a long
// session's recent account activity without dominating the frame.
const maxCashRows = 200

// StatementMonths implements StatementReader.
func (s *Session) StatementMonths(filter StatementFilter) ([]MonthView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filter.Limit <= 0 {
		filter.Limit = DefaultStatementMonths
	}
	if filter.Limit > MaxStatementRows {
		filter.Limit = MaxStatementRows
	}
	ticksPerMonth := s.world.TicksPerMonth()
	current := s.world.MonthForTick(s.world.Tick)
	if filter.Month != nil && *filter.Month > current {
		return nil, &ErrMonthNotStarted{Month: *filter.Month, Current: current}
	}
	lo, hi := statementWindow(filter, ticksPerMonth, s.world.Tick)
	first := s.world.MonthForTick(lo)
	last := s.world.MonthForTick(hi)
	if last-first+1 > filter.Limit {
		first = last - filter.Limit + 1
	}
	if first < 1 {
		first = 1
	}
	if last < first {
		return []MonthView{}, nil
	}
	return monthRange(s.world.Ledger.Months, first, last, ticksPerMonth, filter.Category), nil
}

// StatementCashEntries implements StatementReader.
func (s *Session) StatementCashEntries(filter StatementFilter) ([]CashEntryView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMonthLocked(filter); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = DefaultCashRows
	}
	if filter.Limit > MaxStatementRows {
		filter.Limit = MaxStatementRows
	}
	lo, hi := statementWindow(filter, s.world.TicksPerMonth(), s.world.Tick)
	return cashEntriesWindow(s.world.Ledger.Cash, lo, hi, filter.Category, filter.CarID, filter.Limit), nil
}

// StatementCars implements StatementReader.
func (s *Session) StatementCars(filter StatementFilter) ([]CarLedgerView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMonthLocked(filter); err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = DefaultCarRows
	}
	if filter.Limit > MaxStatementRows {
		filter.Limit = MaxStatementRows
	}
	lo, hi := statementWindow(filter, s.world.TicksPerMonth(), s.world.Tick)
	return carLedgers(s.world.Ledger.Cash, lo, hi, filter.Category, filter.Limit), nil
}

// checkMonthLocked rejects a read for a month that has not started, so a typo
// cannot look like a quiet month.
func (s *Session) checkMonthLocked(filter StatementFilter) error {
	if filter.Month == nil {
		return nil
	}
	current := s.world.MonthForTick(s.world.Tick)
	if *filter.Month > current {
		return &ErrMonthNotStarted{Month: *filter.Month, Current: current}
	}
	return nil
}

// DefaultCarRows bounds the per-car aggregate when the caller names no limit.
const DefaultCarRows = 50

// carLedgers aggregates statement rows per car over the window, highest row
// count first, id as the tiebreak. Rows without a car are world costs and are
// deliberately absent: they are not any vehicle's profit or loss.
func carLedgers(entries []engine.CashEntry, lo, hi int64, category string, limit int) []CarLedgerView {
	type total struct {
		name     string
		income   int
		expenses int
		rows     int
	}
	sums := map[string]*total{}
	order := make([]string, 0, 8)
	for _, e := range entries {
		if e.Tick < lo || e.Tick > hi || e.CarID == "" {
			continue
		}
		if category != "" && !strings.EqualFold(string(e.Kind.Category()), category) {
			continue
		}
		t, ok := sums[e.CarID]
		if !ok {
			t = &total{name: e.CarName}
			sums[e.CarID] = t
			order = append(order, e.CarID)
		}
		if e.CarName != "" {
			t.name = e.CarName
		}
		if e.Amount > 0 {
			t.income += e.Amount
		} else if e.Amount < 0 {
			t.expenses += -e.Amount
		}
		t.rows++
	}
	out := make([]CarLedgerView, 0, len(order))
	for _, id := range order {
		t := sums[id]
		out = append(out, CarLedgerView{
			CarID: id, CarName: t.name,
			Income: t.income, Expenses: t.expenses,
			Net: t.income - t.expenses, Rows: t.rows,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rows != out[j].Rows {
			return out[i].Rows > out[j].Rows
		}
		return out[i].CarID < out[j].CarID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// statementWindow resolves a filter into an inclusive tick window. The window
// never extends past the current tick: there is no history in the future, and
// clamping keeps a bogus month from producing a page of zero rows.
func statementWindow(filter StatementFilter, ticksPerMonth int, currentTick int64) (lo, hi int64) {
	lo, hi = 0, currentTick
	if filter.Month != nil {
		month := int64(*filter.Month)
		lo = (month - 1) * int64(ticksPerMonth)
		hi = lo + int64(ticksPerMonth) - 1
	}
	if filter.FromTick != nil && int64(*filter.FromTick) > lo {
		lo = int64(*filter.FromTick)
	}
	if filter.ToTick != nil && int64(*filter.ToTick) < hi {
		hi = int64(*filter.ToTick)
	}
	if hi > currentTick {
		hi = currentTick
	}
	if lo < 0 {
		lo = 0
	}
	return lo, hi
}

// monthRange renders months first..last newest first, zero-filling months the
// engine has no summary for. A month with no entries is still a row: skipping
// it would make month 1 followed by month 3 look like a data bug rather than a
// quiet month.
//
// When category is non-empty the row keeps only that bucket and recomputes
// Income, Expenses and Net from it, so a category-filtered month reads as that
// bucket's own report rather than a whole-month total with a filtered list.
func monthRange(summaries []engine.MonthSummary, first, last, ticksPerMonth int, category string) []MonthView {
	byIndex := make(map[int]engine.MonthSummary, len(summaries))
	for _, m := range summaries {
		byIndex[m.Index] = m
	}
	out := make([]MonthView, 0, last-first+1)
	for i := last; i >= first; i-- {
		m, ok := byIndex[i]
		if !ok {
			start := int64(i-1) * int64(ticksPerMonth)
			m = engine.MonthSummary{
				Index: i, StartTick: start, EndTick: start + int64(ticksPerMonth) - 1,
			}
		}
		categories := make([]MonthCategoryView, 0, len(m.Categories))
		income, expenses := m.Income, m.Expenses
		if category != "" {
			income, expenses = 0, 0
		}
		for _, c := range m.Categories {
			if category != "" && !strings.EqualFold(string(c.Category), category) {
				continue
			}
			categories = append(categories, MonthCategoryView{
				Category: string(c.Category),
				Income:   c.Income,
				Expenses: c.Expenses,
			})
			if category != "" {
				income += c.Income
				expenses += c.Expenses
			}
		}
		out = append(out, MonthView{
			Index: m.Index, StartTick: m.StartTick, EndTick: m.EndTick,
			Income: income, Expenses: expenses, Net: income - expenses,
			Categories: categories,
		})
	}
	return out
}

// cashEntriesWindow converts the stored statement to the client shape, newest
// first, keeping at most limit rows whose tick and attributes match.
//
// The engine keeps entries in chronological order so appending is cheap and the
// running balance reads forward; the UI renders an account statement from the
// latest line, so reversing here is the one conversion.
func cashEntriesWindow(entries []engine.CashEntry, lo, hi int64, category, carID string, limit int) []CashEntryView {
	out := make([]CashEntryView, 0, min(len(entries), limit))
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := entries[i]
		if e.Tick < lo || e.Tick > hi {
			continue
		}
		if category != "" && !strings.EqualFold(string(e.Kind.Category()), category) {
			continue
		}
		if carID != "" && e.CarID != carID {
			continue
		}
		out = append(out, CashEntryView{
			Seq:      e.Seq,
			Tick:     e.Tick,
			Kind:     string(e.Kind),
			Category: string(e.Kind.Category()),
			Amount:   e.Amount,
			Balance:  e.Balance,
			CarID:    e.CarID,
			CarName:  e.CarName,
			OrderID:  e.OrderID,
			Memo:     e.Memo,
		})
	}
	return out
}

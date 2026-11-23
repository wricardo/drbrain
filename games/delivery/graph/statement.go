package graph

import (
	"errors"
	"strings"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// statementSource adapts the session's full-history reader to the model's
// filter shape. The two packages share no types on purpose: the session layer
// knows engine names (lower-case cash categories, int64 ticks) while the model
// layer speaks GraphQL enums, and this is the single place the two meet.
type statementSource struct {
	reader session.StatementReader
}

// validateStatementArgs rejects arguments that cannot describe a window. The
// session layer clamps and defaults; only values that are meaningless rather
// than merely large are errors here.
func validateStatementArgs(month, fromTick, toTick *int, limit *int) error {
	if month != nil && *month < 1 {
		return errBadInput("month must be 1 or later")
	}
	if fromTick != nil && *fromTick < 0 {
		return errBadInput("fromTick must not be negative")
	}
	if toTick != nil && *toTick < 0 {
		return errBadInput("toTick must not be negative")
	}
	if fromTick != nil && toTick != nil && *fromTick > *toTick {
		return errBadInput("fromTick must not be after toTick")
	}
	if limit != nil && *limit < 1 {
		return errBadInput("limit must be 1 or later")
	}
	return nil
}

// sessionFilter lowers model arguments to the session filter. The caller must
// have validated them already.
func sessionFilter(filter model.StatementFilter) session.StatementFilter {
	out := session.StatementFilter{
		Month:    filter.Month,
		FromTick: filter.FromTick,
		ToTick:   filter.ToTick,
		Limit:    filter.Limit,
	}
	if filter.Category != nil {
		out.Category = strings.ToLower(string(*filter.Category))
	}
	if filter.CarID != nil {
		out.CarID = *filter.CarID
	}
	return out
}

// statementErr maps the one range error the reader can raise onto the delivery
// error dialect. Everything else passes through unchanged.
func statementErr(err error) error {
	var notStarted *session.ErrMonthNotStarted
	if errors.As(err, &notStarted) {
		return errBadInput(notStarted.Error())
	}
	return err
}

// Months implements model.StatementSource.
func (s statementSource) Months(filter model.StatementFilter) ([]*model.MonthView, error) {
	rows, err := s.reader.StatementMonths(sessionFilter(filter))
	if err != nil {
		return nil, statementErr(err)
	}
	return monthsFrom(rows), nil
}

// CashEntries implements model.StatementSource.
func (s statementSource) CashEntries(filter model.StatementFilter) ([]*model.CashEntryView, error) {
	rows, err := s.reader.StatementCashEntries(sessionFilter(filter))
	if err != nil {
		return nil, statementErr(err)
	}
	return cashEntriesFrom(rows), nil
}

// Cars implements model.StatementSource.
func (s statementSource) Cars(filter model.StatementFilter) ([]*model.CarLedgerView, error) {
	rows, err := s.reader.StatementCars(sessionFilter(filter))
	if err != nil {
		return nil, statementErr(err)
	}
	return carLedgersFrom(rows), nil
}

// carLedgersFrom converts the per-car aggregate into the generated model.
func carLedgersFrom(rows []session.CarLedgerView) []*model.CarLedgerView {
	out := make([]*model.CarLedgerView, 0, len(rows))
	for _, r := range rows {
		out = append(out, &model.CarLedgerView{
			CarID: r.CarID, CarName: r.CarName,
			Income: r.Income, Expenses: r.Expenses, Net: r.Net, Rows: r.Rows,
		})
	}
	return out
}

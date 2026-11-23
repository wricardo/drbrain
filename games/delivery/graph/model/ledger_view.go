package model

// LedgerView is the client view of the checking account and its report.
//
// It is hand-written rather than generated because months and cashEntries take
// filter arguments: gqlgen binds those fields to resolvers instead of struct
// fields (see fields: in games/delivery/gqlgen.yml), and a bound type is never
// emitted into models_gen.go. The struct therefore still carries the fields the
// resolver has no arguments for, plus the bounded windows a snapshot ships so
// the unfiltered case costs nothing.
//
// Keep the exported fields in sync with schema.graphqls: gqlgen binds them by
// name, so a schema field without a matching field here fails generation.
type LedgerView struct {
	// Spendable balance. Buying a car or build reduces it.
	Money int
	// Lifetime earnings; never decreases. Achievements read this, not money.
	MoneyEarned int
	// 0-5, clamped. Grants and deliveries cannot push it past the scale.
	Reputation float64
	// On-time deliveries only. A late delivery increments LateDeliveries instead.
	Deliveries     int
	LateDeliveries int
	ExpiredOrders  int
	// Every order ever raised, including the ones that later expired.
	OrdersSpawned int
	// Consecutive on-time deliveries. Both a late delivery and an expiry reset it to 0.
	OnTimeStreak int
	Tows         int
	// Delivered ORDERS per item, not boxes: a two-box delivery adds 1.
	DeliveriesByItem []*ItemCount
	// Lifetime fleet running cost paid, cumulative. Zero when the map disables
	// upkeep. Not subtracted from moneyEarned: upkeep is a cost, not negative
	// income, and progression is measured by the city's own growth schedule.
	UpkeepPaid int
	// Lifetime spend on tows, cumulative.
	TowFees int
	// What the fleet would be charged on the next upkeep interval.
	UpkeepDue int
	// PayMultiplier is the share of a quoted reward this standing collects.
	PayMultiplier float64
	// CashEntries is the snapshot's bounded statement window, newest first.
	CashEntries []*CashEntryView
	// Length of one accounting month in simulation ticks.
	TicksPerMonth int
	// Months is the snapshot's bounded monthly report, newest first.
	Months []*MonthView

	// Statement serves filtered reads over the full history. Set on views
	// converted from a live session; nil on a hand-built view, which then only
	// exposes the bounded windows above and rejects filtered reads.
	statement StatementSource
}

// StatementSource answers filtered statement reads over a session's complete
// accounting history, newest first. It is implemented outside this package (the
// delivery resolver adapts the session's own reader), so the view stays a plain
// data holder.
type StatementSource interface {
	Months(filter StatementFilter) ([]*MonthView, error)
	CashEntries(filter StatementFilter) ([]*CashEntryView, error)
	Cars(filter StatementFilter) ([]*CarLedgerView, error)
}

// StatementFilter narrows a statement read. The zero value constrains nothing;
// each field is optional.
type StatementFilter struct {
	// Month is the 1-based accounting month. It overrides the tick window with
	// that month's bounds.
	Month *int
	// FromTick and ToTick are inclusive tick bounds, intersected with Month
	// when both are given.
	FromTick *int
	ToTick   *int
	// Category keeps one economic bucket.
	Category *CashCategory
	// CarID keeps rows attributed to one car. Only statement rows have a car.
	CarID *string
	// Limit caps the rows returned. Zero means the caller's default.
	Limit int
}

// SetStatement binds a filtered reader to the view.
func (v *LedgerView) SetStatement(s StatementSource) { v.statement = s }

// Statement returns the bound reader, or nil when the view carries only its
// bounded window.
func (v *LedgerView) Statement() StatementSource { return v.statement }

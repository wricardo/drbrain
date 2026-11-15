package engine

// Ledger is the playthrough's running score: money, reputation and the
// counters that achievements read. It is deliberately a plain struct of
// monotonic-ish totals, so requirement checks never have to interpret the
// world.
//
// Distinction worth keeping straight:
//
//	Money       current balance (spendable, can go down)
//	MoneyEarned lifetime earnings (never decreases; achievements read this)
type Ledger struct {
	Money       int
	MoneyEarned int
	Reputation  float64

	Deliveries       int
	LateDeliveries   int
	ExpiredOrders    int
	OrdersSpawned    int
	OnTimeStreak     int
	Tows             int
	BuildsCompleted  int
	DeliveriesByItem map[string]int

	// Expiry diagnostics: why orders failed, not just that they did.
	ExpiredUnclaimed int // never assigned to any car
	ExpiredAssigned  int // claimed but never picked up in time
	ExpiredInTransit int // picked up but not delivered in time

	// UpkeepPaid is lifetime fleet running cost, and TowFees is lifetime spend
	// on tows. They are separate from Money because Money only shows the
	// current balance: a player cannot tell a profitable fleet from a losing one
	// by watching a number that also goes up when they deliver. These two are the
	// feedback that makes the cost learnable, and they are cumulative so they
	// survive the balance being spent on a build.
	//
	// Neither is subtracted from MoneyEarned: upkeep is a cost, not negative
	// income, and achievements that read money_earned must not be slowed by it.
	UpkeepPaid int
	TowFees    int
}

// NewLedger returns a ledger with the given starting balance.
func NewLedger(startMoney int) *Ledger {
	return &Ledger{
		Money:            startMoney,
		Reputation:       3.0, // neutral starting standing out of 5
		DeliveriesByItem: map[string]int{},
	}
}

// CarsOwned reports the fleet size, including stranded cars.
//
// No caller uses it today: the achievement requirements in progress.go compute
// len(w.Cars) directly, which is the same number. It exists as a stable accessor
// for the `cars_owned` requirement. If a future rule ever needs to count only
// drivable cars, change it here rather than at the two call sites, and be aware
// that doing so would silently change how a stalled fleet reads for achievements.
func (w *World) CarsOwned() int { return len(w.Cars) }

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
}

// NewLedger returns a ledger with the given starting balance.
func NewLedger(startMoney int) *Ledger {
	return &Ledger{
		Money:            startMoney,
		Reputation:       3.0, // neutral starting standing out of 5
		DeliveriesByItem: map[string]int{},
	}
}

// CarsOwned reports the fleet size, which some achievements read.
func (w *World) CarsOwned() int { return len(w.Cars) }

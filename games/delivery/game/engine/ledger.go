package engine

import "sort"

// The game has no wall clock calendar: a month is a fixed slice of simulation
// ticks so monthly totals replay exactly from (seed, commands). The slice
// length is the map's ticks_per_month; the shipped map uses 600, which gives
// the 3000-tick canary run five months at roughly 48 deliveries per month.

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
	// Money is the current spendable balance; purchases, fines, charging,
	// tows and upkeep reduce it.
	Money int
	// MoneyEarned is lifetime income and never decreases, so achievements and
	// progression read it instead of Money.
	MoneyEarned int
	// Reputation is the standing score, clamped to [0, max_reputation].
	Reputation float64

	// Deliveries counts on-time deliveries only; a late dropoff increments
	// LateDeliveries, and an order that never arrives increments ExpiredOrders.
	Deliveries     int
	LateDeliveries int
	ExpiredOrders  int
	// OrdersSpawned counts every order raised this session.
	OrdersSpawned int
	// OnTimeStreak is the run of consecutive on-time deliveries; a late
	// delivery or an expiry resets it.
	OnTimeStreak int
	// Tows counts paid recalls to a depot.
	Tows int
	// DeliveriesByItem counts completed deliveries by item id: late deliveries
	// are included, and the unit is orders, not boxes.
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
	// survive the balance being spent elsewhere.
	//
	// Neither is subtracted from MoneyEarned: upkeep is a cost, not negative
	// income, and achievements that read money_earned must not be slowed by it.
	UpkeepPaid int
	TowFees    int

	// Cash is the checking-account ledger: one entry per movement of Money,
	// oldest first, each carrying the running balance after the movement and
	// the car it belongs to when one does. It is the detailed record the totals
	// above summarise; the totals stay because achievements and the HUD read
	// them without scanning history.
	Cash []CashEntry

	// Months is the month-by-month income/expense summary derived from Cash.
	// It is not serialised: ImportState rebuilds it from the statement, which
	// keeps old saves compatible and prevents two sources of truth. The
	// in-memory value is maintained incrementally by recordCash so snapshots do
	// not rescan the whole history on every tick.
	Months []MonthSummary `json:"-"`

	// NextCashSeq is the id handed to the next entry. Persisted so two entries
	// can never share a seq across a save/restore.
	NextCashSeq int

	// ReputationEntries is the append-only standing history: one row per
	// reputation change, oldest first, with the running balance after it.
	// New saves also carry an opening row from newStartLedger.
	ReputationEntries []ReputationEntry `json:"reputation_entries,omitempty"`
	// NextReputationSeq is the id handed to the next reputation entry.
	NextReputationSeq int `json:"next_reputation_seq,omitempty"`
}

// ReputationReason categorises one change to the standing score.
type ReputationReason string

const (
	ReputationOpening      ReputationReason = "opening"
	ReputationDelivery     ReputationReason = "delivery"
	ReputationLateDelivery ReputationReason = "late_delivery"
	ReputationExpired      ReputationReason = "expired"
	ReputationGrowth       ReputationReason = "growth"
)

// ReputationEntry is one movement of the standing score.
type ReputationEntry struct {
	Seq     int              `json:"seq"`
	Tick    int64            `json:"tick"`
	Delta   float64          `json:"delta"`
	Balance float64          `json:"balance"`
	Reason  ReputationReason `json:"reason"`
	OrderID string           `json:"order_id,omitempty"`
	Memo    string           `json:"memo,omitempty"`
}

// CashEntryKind categorises one movement through the player's balance.
type CashEntryKind string

const (
	CashOpening      CashEntryKind = "opening"
	CashDelivery     CashEntryKind = "delivery"
	CashLateDelivery CashEntryKind = "late_delivery"
	CashOrderFine    CashEntryKind = "order_fine"
	CashUpkeep       CashEntryKind = "upkeep"
	CashCharging     CashEntryKind = "charging"
	CashTow          CashEntryKind = "tow"
	CashCarPurchase  CashEntryKind = "car_purchase"
	CashCarSale      CashEntryKind = "car_sale"
	CashGrant        CashEntryKind = "grant"

	// CashBuild records player-bought chargers (BuyCharger). City expansion
	// itself is a scheduled growth stage, not a purchase; older statements
	// also keep their BUILD rows under this kind.
	CashBuild CashEntryKind = "build"
)

// CashCategory groups statement kinds into the buckets the monthly report
// shows. Kinds stay the detailed label on each row; categories are what the
// income/expense breakdown totals. Opening capital is deliberately its own
// category and is excluded from monthly income: starting money is not earnings.
type CashCategory string

const (
	CashCategoryOpening        CashCategory = "opening"
	CashCategoryDeliveryIncome CashCategory = "delivery_income"
	CashCategoryRewards        CashCategory = "rewards"
	CashCategoryOrderFines     CashCategory = "order_fines"
	CashCategoryUpkeep         CashCategory = "upkeep"
	CashCategoryCharging       CashCategory = "charging"
	CashCategoryTows           CashCategory = "tows"
	CashCategoryCarPurchases   CashCategory = "car_purchases"
	CashCategoryCarSales       CashCategory = "car_sales"
	CashCategoryBuilds         CashCategory = "builds"
	CashCategoryOther          CashCategory = "other"
)

// Category maps a statement kind to its monthly bucket. Unknown kinds fall
// into other rather than disappearing from the totals, so a newly added kind
// cannot make the monthly report disagree with the statement.
func (k CashEntryKind) Category() CashCategory {
	switch k {
	case CashOpening:
		return CashCategoryOpening
	case CashDelivery, CashLateDelivery:
		return CashCategoryDeliveryIncome
	case CashGrant:
		return CashCategoryRewards
	case CashOrderFine:
		return CashCategoryOrderFines
	case CashUpkeep:
		return CashCategoryUpkeep
	case CashCharging:
		return CashCategoryCharging
	case CashTow:
		return CashCategoryTows
	case CashCarPurchase:
		return CashCategoryCarPurchases
	case CashCarSale:
		return CashCategoryCarSales
	case CashBuild:
		return CashCategoryBuilds
	}
	return CashCategoryOther
}

// cashCategoryOrder is the deterministic report order. It is a slice rather
// than a map iteration so a monthly breakdown does not reshuffle between
// snapshots.
var cashCategoryOrder = []CashCategory{
	CashCategoryDeliveryIncome,
	CashCategoryRewards,
	CashCategoryOrderFines,
	CashCategoryUpkeep,
	CashCategoryCharging,
	CashCategoryTows,
	CashCategoryCarPurchases,
	CashCategoryCarSales,
	CashCategoryBuilds,
	CashCategoryOther,
	CashCategoryOpening,
}

func cashCategoryRank(c CashCategory) int {
	for i, candidate := range cashCategoryOrder {
		if candidate == c {
			return i
		}
	}
	return len(cashCategoryOrder)
}

// MonthSummary is one accounting month of the checking account.
//
// Income and Expenses are positive magnitudes; Net is derived by the view.
// Opening capital does not appear here, and a month with no real movement is
// represented by a zero summary when the view fills gaps.
type MonthSummary struct {
	// Index is the 1-based accounting month.
	Index int
	// StartTick and EndTick are inclusive tick bounds.
	StartTick int64
	EndTick   int64
	Income    int
	Expenses  int
	// Categories is the per-category breakdown; Other may carry both sides.
	Categories []MonthCategory
}

// MonthCategory is one category's movement within a month. A normal category
// has either Income or Expenses set, but Other can carry both.
type MonthCategory struct {
	Category CashCategory
	Income   int
	Expenses int
}

// monthForTick returns the 1-based accounting month containing tick.
func monthForTick(tick, ticksPerMonth int64) int {
	if tick < 0 {
		return 1
	}
	return int(tick/ticksPerMonth) + 1
}

// monthBounds returns the half-open tick range of a 1-based month.
func monthBounds(month int, ticksPerMonth int64) (start, end int64) {
	start = int64(month-1) * ticksPerMonth
	return start, start + ticksPerMonth - 1
}

// addCashToMonth folds one recorded entry into the in-memory monthly summary.
// Opening capital is skipped; every other movement contributes to exactly one
// side of the month and to exactly one category.
func (l *Ledger) addCashToMonth(e CashEntry, ticksPerMonth int64) {
	category := e.Kind.Category()
	if category == CashCategoryOpening {
		return
	}
	month := monthForTick(e.Tick, ticksPerMonth)
	for len(l.Months) < month {
		index := len(l.Months) + 1
		start, end := monthBounds(index, ticksPerMonth)
		l.Months = append(l.Months, MonthSummary{
			Index: index, StartTick: start, EndTick: end,
		})
	}
	m := &l.Months[month-1]
	if e.Amount > 0 {
		m.Income += e.Amount
	} else if e.Amount < 0 {
		m.Expenses += -e.Amount
	}
	for i := range m.Categories {
		if m.Categories[i].Category != category {
			continue
		}
		if e.Amount > 0 {
			m.Categories[i].Income += e.Amount
		} else if e.Amount < 0 {
			m.Categories[i].Expenses += -e.Amount
		}
		return
	}
	c := MonthCategory{Category: category}
	if e.Amount > 0 {
		c.Income = e.Amount
	} else if e.Amount < 0 {
		c.Expenses = -e.Amount
	}
	m.Categories = append(m.Categories, c)
	sort.SliceStable(m.Categories, func(i, j int) bool {
		return cashCategoryRank(m.Categories[i].Category) < cashCategoryRank(m.Categories[j].Category)
	})
}

// rebuildCashMonths re-derives the monthly summaries from the statement.
// ImportState calls it so an old save (or a ledger built by hand in a test)
// gets the same report as a live session.
func (l *Ledger) rebuildCashMonths(ticksPerMonth int64) {
	l.Months = nil
	for _, e := range l.Cash {
		l.addCashToMonth(e, ticksPerMonth)
	}
}

// CashEntry is one line of the checking-account ledger.
//
// Amount is signed: deliveries and grants are positive, every purchase and fee
// is negative. Balance is the running balance AFTER the movement, so a client
// can render an account statement without replaying history. Amount is what
// was actually charged, which is not always what was billed: fines, upkeep and
// tows clamp at a zero balance, and a charge of $0 records no entry at all.
//
// CarID/CarName are filled for movements that are about one car: a delivery
// (the car that dropped it off), upkeep (the car that was billed), a charging
// session, a tow, and a car purchase (the car bought). Fleet-wide movements
// (grants, achievements) leave both empty. CarName is a snapshot so a statement
// stays readable without joining against the current fleet.
type CashEntry struct {
	Seq     int           `json:"seq"`
	Tick    int64         `json:"tick"`
	Kind    CashEntryKind `json:"kind"`
	Amount  int           `json:"amount"`
	Balance int           `json:"balance"`
	CarID   string        `json:"car_id,omitempty"`
	CarName string        `json:"car_name,omitempty"`
	OrderID string        `json:"order_id,omitempty"`
	Memo    string        `json:"memo,omitempty"`
}

// NewLedger returns a ledger with the given starting balance: Money is set to
// startMoney, a non-zero balance is recorded as the opening cash entry, and
// Lifetime MoneyEarned starts at zero, so starting capital is not counted as
// earnings.
//
// Reputation starts at a neutral 3.0 so the type is usable without a map
// config (tests, fixtures). A real world does not keep that value: NewWorld
// opens it at the map's max_reputation via newStartLedger.
func NewLedger(startMoney int) *Ledger {
	l := &Ledger{
		Money:             startMoney,
		Reputation:        3.0, // neutral default; NewWorld starts at max_reputation
		DeliveriesByItem:  map[string]int{},
		NextCashSeq:       1,
		NextReputationSeq: 1,
	}
	if startMoney != 0 {
		l.Cash = append(l.Cash, CashEntry{
			Seq:     0,
			Kind:    CashOpening,
			Amount:  startMoney,
			Balance: startMoney,
			Memo:    "opening balance",
		})
	}
	return l
}

// recordReputation applies one standing change and appends the actual delta to
// the history. The actual delta, not the nominal config amount, is stored so a
// clamp at 0 or max_reputation does not make the series lie. A clamped-to-zero
// change records nothing: the line did not move.
func (w *World) recordReputation(delta float64, reason ReputationReason, orderID, memo string) float64 {
	if delta == 0 {
		return 0
	}
	before := w.Ledger.Reputation
	w.Ledger.Reputation += delta
	w.clampReputation()
	applied := w.Ledger.Reputation - before
	if applied == 0 {
		return 0
	}
	if w.Ledger.NextReputationSeq < 1 {
		w.Ledger.NextReputationSeq = 1
	}
	w.Ledger.ReputationEntries = append(w.Ledger.ReputationEntries, ReputationEntry{
		Seq:     w.Ledger.NextReputationSeq,
		Tick:    w.Tick,
		Delta:   applied,
		Balance: w.Ledger.Reputation,
		Reason:  reason,
		OrderID: orderID,
		Memo:    memo,
	})
	w.Ledger.NextReputationSeq++
	return applied
}

// recordCash applies one movement to the balance and appends it to the ledger.
//
// It returns the signed amount that actually moved, which is what the counters
// (UpkeepPaid, TowFees) must add: a $12 fine against a $3 balance charges $3,
// and the statement shows $3 so the running balance still adds up. A zero
// movement records nothing, because a transaction that moved no money is not a
// line in a checking account.
//
// Every money mutation in the engine goes through here. If a new one does not,
// the balance still changes but the statement silently loses the line, so add
// the call at the mutation site rather than deriving entries later.
func (w *World) recordCash(kind CashEntryKind, amount int, car *Car, orderID, memo string) int {
	if amount == 0 {
		return 0
	}
	applied := amount
	if applied < 0 {
		// A spend cannot take the balance below zero. Clamp to what is there,
		// which is what every other money rule in the district already does.
		available := w.Ledger.Money
		if available < 0 {
			available = 0
		}
		if -applied > available {
			applied = -available
		}
	}
	if applied == 0 {
		return 0
	}
	w.Ledger.Money += applied
	if w.Ledger.Money < 0 {
		w.Ledger.Money = 0
	}

	entry := CashEntry{
		Seq:     w.Ledger.NextCashSeq,
		Tick:    w.Tick,
		Kind:    kind,
		Amount:  applied,
		Balance: w.Ledger.Money,
		OrderID: orderID,
		Memo:    memo,
	}
	if car != nil {
		entry.CarID = car.ID
		entry.CarName = car.Name
	}
	w.Ledger.NextCashSeq++
	w.Ledger.Cash = append(w.Ledger.Cash, entry)
	w.Ledger.addCashToMonth(entry, w.ticksPerMonth)
	return applied
}

// CarsOwned returns the fleet size, including stranded and parked cars. It is
// the stable accessor for the `cars_owned` requirement; changing it to count
// only drivable cars would silently change what that requirement means.
func (w *World) CarsOwned() int { return len(w.Cars) }

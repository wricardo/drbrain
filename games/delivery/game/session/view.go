package session

import (
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// Snapshot returns the full client view under the session lock.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := BuildSnapshot(s.world)
	snap.ID = s.ID
	snap.City.ID = s.cityID
	snap.CreatedAt = s.CreatedAt
	snap.Speed = s.speed
	snap.Paused = s.speed == SpeedPaused
	snap.DisplayName = s.displayName
	snap.Ledger.Statement = s
	return snap
}

// Summary returns the list view of one session: enough to render a save picker
// without loading the world.
//
// Loaded separates a session that is in memory (and therefore ticking) from one
// that exists only as a file. This method always reports true, because it reads
// a live session; Manager.List reports false for the on-disk-only rows.
func (s *Session) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Summary{
		ID:          s.ID,
		CreatedAt:   s.CreatedAt,
		Tick:        s.world.Tick,
		Speed:       s.speed,
		Money:       s.world.Ledger.Money,
		Reputation:  s.world.Ledger.Reputation,
		Deliveries:  s.world.Ledger.Deliveries,
		Loaded:      true,
		AccountID:   s.accountID,
		CityID:      s.cityID,
		DisplayName: s.displayName,
	}
}

// Snapshot is the full client-facing view of a session at one tick.
//
// It is a plain DTO with no engine types leaking through, so the JSON shape is
// a deliberate contract rather than an accident of struct tags. Every field a
// client needs to render the city or to decide a move is here.
type Snapshot struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// DisplayName is the player's name for this save; "" means unnamed, which
	// every view renders as the city name.
	DisplayName string `json:"display_name,omitempty"`

	Tick   int64 `json:"tick"`
	Speed  int   `json:"speed"`
	Paused bool  `json:"paused"`
	TickMS int   `json:"tick_ms"`

	City   CityView    `json:"city"`
	Cars   []CarView   `json:"cars"`
	Orders []OrderView `json:"orders"`
	Ledger LedgerView  `json:"ledger"`

	// CarClasses is the full catalogue with per-session unlock/affordability
	// flags, so a client can offer exactly the purchases the server would allow.
	CarClasses []CarClassView `json:"car_classes"`

	// Growth is the city's scheduled expansion: which stages are still ahead,
	// when they fire, and what each one opens. The client renders this as a
	// read-only timeline.
	Growth []GrowthView `json:"growth"`

	Events []EventView `json:"events"`
	// EventCapacity is how many events the retained feed can hold. Events
	// older than the oldest retained row are gone, and without this a poller
	// cannot tell a quiet stretch from a gap it missed.
	EventCapacity int `json:"event_capacity"`

	// Closures are the street works live right now. Empty when none are, so a
	// client can treat "no closures" as the common case without a nil check.
	Closures []ClosureView `json:"closures"`
}

// CityView is the terrain plus the entities on it. Kitchens, depots and
// chargers are fixed for the session; Customers is the live set of sites
// currently waiting for a delivery (World.PresentCustomers), so it changes tick
// to tick. Rows already render a waiting site as 'H' and an idle house as 'h'.
type CityView struct {
	// ID is the catalogue id when the view came from a running session.
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	GridSize int      `json:"grid_size"`
	Rows     []string `json:"rows"`
	Hidden   int      `json:"hidden_tiles"`

	Kitchens  []PointView `json:"kitchens"`
	Customers []PointView `json:"customers"`
	Depots    []PointView `json:"depots"`
	Chargers  []PointView `json:"chargers"`
}

// PointView is a named entity at a coordinate.
type PointView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// CarView is one car in a snapshot. Capacity, Load and FreeBoxes are measured in
// boxes, not orders; BatteryPct truncates to an integer. State is the coarse
// rule status, Queued is the direction that will be attempted on the next tick,
// and LastReject is the last failed-move code ("" after a successful step).
type CarView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Class      string   `json:"class"`
	X          int      `json:"x"`
	Y          int      `json:"y"`
	Battery    int      `json:"battery"`
	MaxBattery int      `json:"max_battery"`
	BatteryPct int      `json:"battery_pct"`
	Capacity   int      `json:"capacity"`
	Load       int      `json:"load"`
	FreeBoxes  int      `json:"free_boxes"`
	Cargo      []string `json:"cargo"`
	State      string   `json:"state"`
	Queued     string   `json:"queued_move,omitempty"`
	// QueuedMoves is the full movement plan in execution order: the step for
	// the next tick first. Omitted when nothing is queued.
	QueuedMoves []string `json:"queued_moves,omitempty"`
	LastReject  string   `json:"last_reject,omitempty"`

	// ChargeDue is the unpaid balance of a charging session in progress. It is
	// zero on an uncharged car and after the session settles, so a client can
	// show the running cost without inventing a per-tick estimate.
	ChargeDue int `json:"charge_due,omitempty"`
}

// CarClassView is one purchasable vehicle archetype. Exposing the catalogue
// matters: the UI must never hardcode prices, or it will quote a cost the
// server does not charge.
type CarClassView struct {
	Class      string `json:"class"`
	Price      int    `json:"price"`
	Capacity   int    `json:"capacity"`
	MaxBattery int    `json:"max_battery"`
	StepCost   int    `json:"step_cost"`
	Unlocked   bool   `json:"unlocked"`
	Affordable bool   `json:"affordable"`
}

// OrderView is one delivery job. TicksLeft is signed: a negative value means the
// deadline has passed but the order has not been swept yet. Reward is the
// on-time pay, Paid is what was actually paid (the late rate after the
// deadline), and State is the lifecycle position.
type OrderView struct {
	ID           string `json:"id"`
	Item         string `json:"item"`
	ItemName     string `json:"item_name"`
	Quantity     int    `json:"quantity"`
	KitchenID    string `json:"kitchen_id"`
	KitchenName  string `json:"kitchen_name"`
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
	State        string `json:"state"`
	AssignedCar  string `json:"assigned_car,omitempty"`
	CreatedTick  int64  `json:"created_tick"`
	DeadlineTick int64  `json:"deadline_tick"`
	TicksLeft    int64  `json:"ticks_left"`
	Reward       int    `json:"reward"`
	Paid         int    `json:"paid,omitempty"`
	Late         bool   `json:"late,omitempty"`
}

// ClosureView is one stretch of street closed for works.
//
// TicksLeft is derived, not stored: EndTick - now.
//
// The minimum observed value is 1, never 0. The closure window is half-open
// (ActiveAt is StartTick <= tick < EndTick), so the closure is already absent
// from the snapshot on the tick it counts down to; there is no snapshot in which
// TicksLeft is 0. A countdown UI should therefore read 1 as "reopens next tick"
// rather than expecting to see the street reopen while a closure is still listed.
//
// The bounding box (MinX..MaxY) is inclusive and exists so a client can decide
// which map chunk to repaint without iterating Tiles, which is the authoritative
// set. A closure is always a straight run along one street, so one of the two
// spans is 1 wide.
type ClosureView struct {
	ID        string      `json:"id"`
	Reason    string      `json:"reason"`
	StartTick int64       `json:"start_tick"`
	EndTick   int64       `json:"end_tick"`
	TicksLeft int64       `json:"ticks_left"`
	Tiles     []PointView `json:"tiles"`
	MinX      int         `json:"min_x"`
	MinY      int         `json:"min_y"`
	MaxX      int         `json:"max_x"`
	MaxY      int         `json:"max_y"`
}

// LedgerView is money, reputation and the counters the economy reads.
//
// Deliveries excludes late ones: a late dropoff increments LateDeliveries
// instead, so a client computing "jobs failed" must not read Deliveries alone.
// MoneyEarned is lifetime and never decreases, which is why progression and
// reporting read it rather than Money — upkeep and tows must not undo it.
//
// DeliveriesByItem is keyed by item id and counts ORDERS, not boxes. It is
// incremented once per settled delivery, so a two-box order adds 1. Verified
// end to end: delivering a quantity-2 pizza order gave
// deliveriesByItem [{pizza, 1}] and deliveries 1. Do not multiply by quantity to
// recover volume; a box count has to be summed from the orders themselves.
type LedgerView struct {
	Money            int            `json:"money"`
	MoneyEarned      int            `json:"money_earned"`
	Reputation       float64        `json:"reputation"`
	Deliveries       int            `json:"deliveries"`
	LateDeliveries   int            `json:"late_deliveries"`
	ExpiredOrders    int            `json:"expired_orders"`
	OrdersSpawned    int            `json:"orders_spawned"`
	OnTimeStreak     int            `json:"on_time_streak"`
	Tows             int            `json:"tows"`
	DeliveriesByItem map[string]int `json:"deliveries_by_item"`

	// Running-cost feedback. UpkeepDue is what the fleet would be charged on
	// the next interval, so a client can show the drain before it lands rather
	// than only after it has already happened.
	UpkeepPaid int `json:"upkeep_paid"`
	TowFees    int `json:"tow_fees"`
	UpkeepDue  int `json:"upkeep_due"`

	// PayMultiplier is the share of the quoted reward this standing collects.
	// The client shows it next to reputation so a reduced payout is explained
	// rather than looking like a short-paid order.
	PayMultiplier float64 `json:"pay_multiplier"`

	// CashEntries is the checking-account statement, newest first: the newest
	// DefaultStatementMonths window capped at maxCashRows. The query and live
	// stream carry the same rows (the stream trims order history, not the
	// statement). Non-nil so an empty statement marshals as [] rather than null.
	CashEntries []CashEntryView `json:"cash_entries"`

	// TicksPerMonth and Months are the monthly income/expense report. It is
	// newest first, bounded to DefaultStatementMonths; the engine keeps the
	// full derived history and the view fills quiet months with zero rows so
	// the player can see a gap rather than silently skipping a month number.
	TicksPerMonth int         `json:"ticks_per_month"`
	Months        []MonthView `json:"months"`

	// Statement serves filtered reads over the full history, so a client can
	// ask for an older month or a tick range without the frame carrying it.
	// Set on snapshots built from a live session; nil on a hand-built view,
	// which then exposes only the bounded window above.
	Statement StatementReader `json:"-"`
}

// CashEntryView is one movement through the balance.
//
// Amount is signed; Balance is the running balance after the movement. CarID
// and CarName are set when the movement belongs to one vehicle, so a statement
// row can be attributed without the client joining the fleet list.
type CashEntryView struct {
	Seq      int    `json:"seq"`
	Tick     int64  `json:"tick"`
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Amount   int    `json:"amount"`
	Balance  int    `json:"balance"`
	CarID    string `json:"car_id,omitempty"`
	CarName  string `json:"car_name,omitempty"`
	OrderID  string `json:"order_id,omitempty"`
	Memo     string `json:"memo,omitempty"`
}

// MonthView is one accounting month of income and expenses. Index is 1-based;
// StartTick and EndTick are inclusive.
type MonthView struct {
	Index      int                 `json:"index"`
	StartTick  int64               `json:"start_tick"`
	EndTick    int64               `json:"end_tick"`
	Income     int                 `json:"income"`
	Expenses   int                 `json:"expenses"`
	Net        int                 `json:"net"`
	Categories []MonthCategoryView `json:"categories"`
}

// MonthCategoryView is one category's contribution to a month. Income and
// Expenses are positive magnitudes; a normal category has one side set.
type MonthCategoryView struct {
	Category string `json:"category"`
	Income   int    `json:"income"`
	Expenses int    `json:"expenses"`
}

// GrowthView is one stage of the city's expansion schedule.
type GrowthView struct {
	ID          string `json:"id"`
	Tick        int64  `json:"tick"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Done        bool   `json:"done"`
}

// EventView is one entry of the feed.
type EventView struct {
	Tick    int64  `json:"tick"`
	Type    string `json:"type"`
	Message string `json:"message"`
	CarID   string `json:"car_id,omitempty"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
}

// BuildSnapshot converts a world into the client view. Call inside Do so the
// read is consistent with the tick loop.
func BuildSnapshot(w *engine.World) Snapshot {
	city := w.City
	// Only customers with a live order are on the map: a site pops in when it
	// raises one and leaves when it is delivered or expires (see
	// engine.World.PresentCustomers). The house tiles stay as 'h' between visits.
	home := w.PresentCustomers()
	rows := city.RenderRowsAtHome(w.RenderCars(), w.ClosedSet(), home)

	snap := Snapshot{
		Tick:   w.Tick,
		TickMS: city.TickMS,
		// Non-nil slices: an empty game must marshal as [] not null, so client
		// code can iterate without a nil guard on every list.
		Cars:       []CarView{},
		Orders:     []OrderView{},
		CarClasses: []CarClassView{},
		Growth:     []GrowthView{},
		Events:     []EventView{},
		Closures:   []ClosureView{},
		City: CityView{
			Name:      city.Name,
			GridSize:  city.GridSize,
			Rows:      rows,
			Hidden:    city.HiddenCount(),
			Kitchens:  []PointView{},
			Customers: []PointView{},
			Depots:    []PointView{},
			Chargers:  []PointView{},
		},
	}

	for _, k := range city.Kitchens {
		snap.City.Kitchens = append(snap.City.Kitchens, PointView{ID: k.ID, Name: k.Name, X: k.Pos.X, Y: k.Pos.Y})
	}
	for _, c := range home {
		snap.City.Customers = append(snap.City.Customers, PointView{ID: c.ID, Name: c.Name, X: c.Pos.X, Y: c.Pos.Y})
	}
	for _, d := range city.Depots {
		snap.City.Depots = append(snap.City.Depots, PointView{ID: d.ID, X: d.Pos.X, Y: d.Pos.Y})
	}
	for _, ch := range city.Chargers {
		snap.City.Chargers = append(snap.City.Chargers, PointView{ID: ch.ID, X: ch.Pos.X, Y: ch.Pos.Y})
	}

	for _, c := range w.Cars {
		snap.Cars = append(snap.Cars, CarView{
			ID: c.ID, Name: c.Name, Class: c.ClassName,
			X: c.Pos.X, Y: c.Pos.Y,
			Battery: c.Battery, MaxBattery: c.Class.MaxBattery,
			BatteryPct:  c.BatteryPct(),
			Capacity:    c.Class.Capacity,
			Load:        c.Load(),
			FreeBoxes:   c.FreeBoxes(),
			Cargo:       nonNilStrings(c.Cargo),
			State:       string(c.State(city)),
			Queued:      string(c.QueuedMove),
			QueuedMoves: queuedDirections(c),
			LastReject:  string(c.LastReject),
			ChargeDue:   c.ChargeDue,
		})
	}

	for _, o := range w.OrdersList() {
		kitchenName, customerName := "", ""
		for _, k := range city.Kitchens {
			if k.ID == o.KitchenID {
				kitchenName = k.Name
			}
		}
		for _, c := range city.Customers {
			if c.ID == o.CustomerID {
				customerName = c.Name
			}
		}
		snap.Orders = append(snap.Orders, OrderView{
			ID: o.ID, Item: o.Item,
			ItemName:     itemName(city, o.Item),
			Quantity:     o.Boxes(),
			KitchenID:    o.KitchenID,
			KitchenName:  kitchenName,
			CustomerID:   o.CustomerID,
			CustomerName: customerName,
			State:        string(o.State),
			AssignedCar:  o.AssignedCar,
			CreatedTick:  o.CreatedTick,
			DeadlineTick: o.DeadlineTick,
			TicksLeft:    o.TicksLeft(w.Tick),
			Reward:       o.Reward,
			Paid:         o.Paid,
			Late:         o.Late,
		})
	}

	// The snapshot carries the newest DefaultStatementMonths of the report and
	// the rows of those months, capped by maxCashRows. Older history stays in
	// the ledger and is served on demand through StatementReader, so a frame
	// never pays for years of rows.
	ticksPerMonth := w.TicksPerMonth()
	lastMonth := w.MonthForTick(w.Tick)
	firstMonth := lastMonth - DefaultStatementMonths + 1
	if firstMonth < 1 {
		firstMonth = 1
	}
	windowStart := int64(firstMonth-1) * int64(ticksPerMonth)

	snap.Ledger = LedgerView{
		Money:            w.Ledger.Money,
		MoneyEarned:      w.Ledger.MoneyEarned,
		Reputation:       w.Ledger.Reputation,
		Deliveries:       w.Ledger.Deliveries,
		LateDeliveries:   w.Ledger.LateDeliveries,
		ExpiredOrders:    w.Ledger.ExpiredOrders,
		OrdersSpawned:    w.Ledger.OrdersSpawned,
		OnTimeStreak:     w.Ledger.OnTimeStreak,
		Tows:             w.Ledger.Tows,
		DeliveriesByItem: w.Ledger.DeliveriesByItem,
		UpkeepPaid:       w.Ledger.UpkeepPaid,
		TowFees:          w.Ledger.TowFees,
		UpkeepDue:        w.FleetUpkeep(),
		PayMultiplier:    w.PayMultiplier(),
		CashEntries:      cashEntriesWindow(w.Ledger.Cash, windowStart, w.Tick, "", "", maxCashRows),
		TicksPerMonth:    ticksPerMonth,
		Months:           monthRange(w.Ledger.Months, firstMonth, lastMonth, ticksPerMonth, ""),
	}

	for _, cc := range w.CarClassCatalog() {
		snap.CarClasses = append(snap.CarClasses, CarClassView{
			Class:      cc.Class,
			Price:      cc.Price,
			Capacity:   cc.Capacity,
			MaxBattery: cc.MaxBattery,
			StepCost:   cc.StepCost,
			Unlocked:   cc.Unlocked,
			Affordable: cc.Affordable,
		})
	}

	for _, g := range w.Growth() {
		snap.Growth = append(snap.Growth, GrowthView{
			ID: g.ID, Tick: g.Tick, Title: g.Title,
			Description: g.Description, Done: g.Done,
		})
	}

	for _, e := range w.Events {
		snap.Events = append(snap.Events, EventView{
			Tick: e.Tick, Type: e.Type, Message: e.Message,
			CarID: e.CarID, X: e.Pos.X, Y: e.Pos.Y,
		})
	}
	snap.EventCapacity = w.MaxEvents()
	for _, c := range w.ActiveClosures() {
		cv := ClosureView{
			ID: c.ID, Reason: c.Reason,
			StartTick: c.StartTick, EndTick: c.EndTick,
			TicksLeft: c.EndTick - w.Tick,
			MinX:      -1, MinY: -1, MaxX: -1, MaxY: -1,
		}
		for i, p := range c.Tiles {
			cv.Tiles = append(cv.Tiles, PointView{X: p.X, Y: p.Y})
			if i == 0 {
				cv.MinX, cv.MinY, cv.MaxX, cv.MaxY = p.X, p.Y, p.X, p.Y
				continue
			}
			cv.MinX = min(cv.MinX, p.X)
			cv.MinY = min(cv.MinY, p.Y)
			cv.MaxX = max(cv.MaxX, p.X)
			cv.MaxY = max(cv.MaxY, p.Y)
		}
		snap.Closures = append(snap.Closures, cv)
	}
	return snap
}

// className was removed: Car now carries ClassName, so the label is the real
// catalogue key rather than a guess from capacity.

// itemName resolves a display name, falling back to the id.
func itemName(city *engine.City, item string) string {
	if n, ok := city.Items[item]; ok && n != "" {
		return n
	}
	return item
}

// nonNilStrings keeps JSON output as [] rather than null, so clients can
// iterate without a nil check.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// queuedDirections renders the full movement plan as strings. The GraphQL view
// is the agent-facing surface, so it exposes the whole backlog and not just the
// step for the next tick.
func queuedDirections(c *engine.Car) []string {
	moves := c.PendingMoves()
	if len(moves) == 0 {
		return nil
	}
	out := make([]string, len(moves))
	for i, d := range moves {
		out[i] = string(d)
	}
	return out
}

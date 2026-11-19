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
	snap.CreatedAt = s.CreatedAt
	snap.Speed = s.speed
	snap.Paused = s.speed == SpeedPaused
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
		ID:         s.ID,
		CreatedAt:  s.CreatedAt,
		Tick:       s.world.Tick,
		Speed:      s.speed,
		Money:      s.world.Ledger.Money,
		Reputation: s.world.Ledger.Reputation,
		Deliveries: s.world.Ledger.Deliveries,
		Loaded:     true,
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

	Achievements []AchievementView `json:"achievements"`
	Unlocks      []UnlockView      `json:"unlocks"`
	Builds       []BuildView       `json:"builds"`

	Events []EventView `json:"events"`

	// Closures are the street works live right now. Empty when none are, so a
	// client can treat "no closures" as the common case without a nil check.
	Closures []ClosureView `json:"closures"`
}

// CityView is the terrain and the fixed entities.
type CityView struct {
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

// CarView is one car.
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
	LastReject string   `json:"last_reject,omitempty"`
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

// OrderView is one delivery job.
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

// LedgerView is money, reputation and the counters achievements read.
//
// Deliveries excludes late ones: a late dropoff increments LateDeliveries
// instead, so a client computing "jobs failed" must not read Deliveries alone.
// MoneyEarned is lifetime and never decreases, which is why achievements test it
// rather than Money — spending a build's cost must not undo progress.
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
	BuildsCompleted  int            `json:"builds_completed"`
	DeliveriesByItem map[string]int `json:"deliveries_by_item"`

	// Running-cost feedback. UpkeepDue is what the fleet would be charged on
	// the next interval, so a client can show the drain before it lands rather
	// than only after it has already happened.
	UpkeepPaid int `json:"upkeep_paid"`
	TowFees    int `json:"tow_fees"`
	UpkeepDue  int `json:"upkeep_due"`
}

// AchievementView is one achievement's state.
type AchievementView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Done        bool   `json:"done"`
	Progress    int    `json:"progress"`
	Target      int    `json:"target"`
}

// UnlockView is one unlock's state.
type UnlockView struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Requires    string   `json:"requires"`
	Earned      bool     `json:"earned"`
	Progress    int      `json:"progress"`
	Target      int      `json:"target"`
	Builds      []string `json:"builds"`
}

// BuildView is one build's availability.
type BuildView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Cost        int    `json:"cost"`
	Unlocked    bool   `json:"unlocked"`
	Completed   bool   `json:"completed"`
	Affordable  bool   `json:"affordable"`
	Applicable  bool   `json:"applicable"`
	Problem     string `json:"problem,omitempty"`
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
	rows := city.RenderRowsWithClosures(w.RenderCars(), w.ClosedSet())

	snap := Snapshot{
		Tick:   w.Tick,
		TickMS: city.TickMS,
		// Non-nil slices: an empty game must marshal as [] not null, so client
		// code can iterate without a nil guard on every list.
		Cars:         []CarView{},
		Orders:       []OrderView{},
		CarClasses:   []CarClassView{},
		Achievements: []AchievementView{},
		Unlocks:      []UnlockView{},
		Builds:       []BuildView{},
		Events:       []EventView{},
		Closures:     []ClosureView{},
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
	for _, c := range city.Customers {
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
			BatteryPct: c.BatteryPct(),
			Capacity:   c.Class.Capacity,
			Load:       c.Load(),
			FreeBoxes:  c.FreeBoxes(),
			Cargo:      nonNilStrings(c.Cargo),
			State:      string(c.State(city)),
			Queued:     string(c.QueuedMove),
			LastReject: string(c.LastReject),
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
		BuildsCompleted:  w.Ledger.BuildsCompleted,
		DeliveriesByItem: w.Ledger.DeliveriesByItem,
		UpkeepPaid:       w.Ledger.UpkeepPaid,
		TowFees:          w.Ledger.TowFees,
		UpkeepDue:        w.FleetUpkeep(),
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

	for _, a := range w.Achievements() {
		snap.Achievements = append(snap.Achievements, AchievementView{
			ID: a.ID, Title: a.Title, Description: a.Description,
			Done: a.Done, Progress: a.Progress, Target: a.Target,
		})
	}
	for _, u := range w.Unlocks() {
		snap.Unlocks = append(snap.Unlocks, UnlockView{
			ID: u.ID, Title: u.Title, Description: u.Description,
			Requires: u.Requires, Earned: u.Earned,
			Progress: u.Progress, Target: u.Target,
			Builds: nonNilStrings(u.Builds),
		})
	}
	for _, b := range w.Builds() {
		snap.Builds = append(snap.Builds, BuildView{
			ID: b.ID, Title: b.Title, Description: b.Description, Cost: b.Cost,
			Unlocked: b.Unlocked, Completed: b.Completed,
			Affordable: b.Affordable, Applicable: b.Applicable, Problem: b.Problem,
		})
	}

	for _, e := range w.Events {
		snap.Events = append(snap.Events, EventView{
			Tick: e.Tick, Type: e.Type, Message: e.Message,
			CarID: e.CarID, X: e.Pos.X, Y: e.Pos.Y,
		})
	}
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

package graph

import (
	"sort"
	"strings"

	"github.com/wricardo/drbrain-delivery/game/session"
	"github.com/wricardo/drbrain-delivery/graph/model"
)

// Conversion between the domain view structs (game/session) and the GraphQL
// model types generated from schema.graphqls.
//
// Why an explicit layer instead of binding the domain structs directly in
// gqlgen.yml: the domain types were shaped by the old JSON tags, so names
// differ (Class vs className) and some shapes differ (map[string]int vs a list
// of ItemCount, plain string vs enum). Both are compiler-checked here, so a
// renamed domain field is a build error rather than a silently missing GraphQL
// field. There is no reflection and no struct tags involved.

// Note on tick narrowing: the domain keeps ticks as int64, while GraphQL's Int
// is 32-bit by spec. The int(...) conversions below are therefore the correct
// mapping, not an oversight. A session would need ~2 billion ticks to overflow,
// which at 300ms/tick is over 19 years of continuous play.

// snapshotFrom converts a full session snapshot.
func snapshotFrom(s session.Snapshot) *model.Snapshot {
	out := &model.Snapshot{
		ID:           s.ID,
		CreatedAt:    s.CreatedAt,
		Tick:         int(s.Tick),
		Speed:        s.Speed,
		Paused:       s.Paused,
		TickMs:       s.TickMS,
		City:         cityFrom(s.City),
		Cars:         make([]*model.CarView, 0, len(s.Cars)),
		Orders:       make([]*model.OrderView, 0, len(s.Orders)),
		Ledger:       ledgerFrom(s.Ledger),
		CarClasses:   make([]*model.CarClassView, 0, len(s.CarClasses)),
		Achievements: make([]*model.AchievementView, 0, len(s.Achievements)),
		Unlocks:      make([]*model.UnlockView, 0, len(s.Unlocks)),
		Builds:       make([]*model.BuildView, 0, len(s.Builds)),
		Events:       make([]*model.EventView, 0, len(s.Events)),
	}
	for _, c := range s.Cars {
		out.Cars = append(out.Cars, carFrom(c))
	}
	for _, o := range s.Orders {
		out.Orders = append(out.Orders, orderFrom(o))
	}
	for _, c := range s.CarClasses {
		out.CarClasses = append(out.CarClasses, &model.CarClassView{
			ClassName:  c.Class,
			Price:      c.Price,
			Capacity:   c.Capacity,
			MaxBattery: c.MaxBattery,
			StepCost:   c.StepCost,
			Unlocked:   c.Unlocked,
			Affordable: c.Affordable,
		})
	}
	for _, a := range s.Achievements {
		out.Achievements = append(out.Achievements, &model.AchievementView{
			ID: a.ID, Title: a.Title, Description: a.Description,
			Done: a.Done, Progress: a.Progress, Target: a.Target,
		})
	}
	for _, u := range s.Unlocks {
		out.Unlocks = append(out.Unlocks, &model.UnlockView{
			ID: u.ID, Title: u.Title, Description: u.Description,
			Earned: u.Earned, Builds: nonNil(u.Builds),
		})
	}
	for _, b := range s.Builds {
		out.Builds = append(out.Builds, &model.BuildView{
			ID: b.ID, Title: b.Title, Description: b.Description, Cost: b.Cost,
			Unlocked: b.Unlocked, Completed: b.Completed,
			Affordable: b.Affordable, Applicable: b.Applicable,
			Problem: optionalString(b.Problem),
		})
	}
	for _, e := range s.Events {
		out.Events = append(out.Events, eventFrom(e))
	}
	return out
}

// cityFrom converts terrain and entities.
func cityFrom(c session.CityView) *model.CityView {
	return &model.CityView{
		Name:        c.Name,
		GridSize:    c.GridSize,
		Rows:        nonNil(c.Rows),
		HiddenTiles: c.Hidden,
		Kitchens:    pointsFrom(c.Kitchens),
		Customers:   pointsFrom(c.Customers),
		Depots:      pointsFrom(c.Depots),
		Chargers:    pointsFrom(c.Chargers),
	}
}

// pointsFrom converts entity positions.
func pointsFrom(ps []session.PointView) []*model.PointView {
	out := make([]*model.PointView, 0, len(ps))
	for _, p := range ps {
		out = append(out, &model.PointView{ID: p.ID, Name: p.Name, X: p.X, Y: p.Y})
	}
	return out
}

// carFrom converts one vehicle, translating the engine's string states into
// the schema's enums. An unrecognised value falls back to the safe default
// rather than failing the whole query: a car with a state the schema does not
// know about should still be visible.
func carFrom(c session.CarView) *model.CarView {
	out := &model.CarView{
		ID:         c.ID,
		Name:       c.Name,
		ClassName:  c.Class,
		X:          c.X,
		Y:          c.Y,
		Battery:    c.Battery,
		MaxBattery: c.MaxBattery,
		BatteryPct: c.BatteryPct,
		Capacity:   c.Capacity,
		Cargo:      nonNil(c.Cargo),
		State:      carStateFrom(c.State),
	}
	if c.Queued != "" {
		if d, ok := directionFrom(c.Queued); ok {
			out.QueuedMove = &d
		}
	}
	if c.LastReject != "" {
		if rr, ok := rejectFrom(c.LastReject); ok {
			out.LastReject = &rr
		}
	}
	return out
}

// orderFrom converts one job.
func orderFrom(o session.OrderView) *model.OrderView {
	out := &model.OrderView{
		ID:           o.ID,
		Item:         o.Item,
		ItemName:     o.ItemName,
		KitchenID:    o.KitchenID,
		KitchenName:  o.KitchenName,
		CustomerID:   o.CustomerID,
		CustomerName: o.CustomerName,
		State:        orderStateFrom(o.State),
		CreatedTick:  int(o.CreatedTick),
		DeadlineTick: int(o.DeadlineTick),
		TicksLeft:    int(o.TicksLeft),
		Reward:       o.Reward,
		Paid:         o.Paid,
		Late:         o.Late,
	}
	if o.AssignedCar != "" {
		car := o.AssignedCar
		out.AssignedCar = &car
	}
	return out
}

// ledgerFrom converts the counters. deliveries_by_item is a map in the domain
// and a list in the schema, sorted by item so the response is stable.
func ledgerFrom(l session.LedgerView) *model.LedgerView {
	var items []*model.ItemCount
	keys := make([]string, 0, len(l.DeliveriesByItem))
	for k := range l.DeliveriesByItem {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		items = append(items, &model.ItemCount{Item: k, Count: l.DeliveriesByItem[k]})
	}
	return &model.LedgerView{
		Money:            l.Money,
		MoneyEarned:      l.MoneyEarned,
		Reputation:       l.Reputation,
		Deliveries:       l.Deliveries,
		LateDeliveries:   l.LateDeliveries,
		ExpiredOrders:    l.ExpiredOrders,
		OrdersSpawned:    l.OrdersSpawned,
		OnTimeStreak:     l.OnTimeStreak,
		Tows:             l.Tows,
		BuildsCompleted:  l.BuildsCompleted,
		DeliveriesByItem: items,
	}
}

// eventFrom converts one feed entry.
func eventFrom(e session.EventView) *model.EventView {
	out := &model.EventView{
		Tick:    int(e.Tick),
		Type:    e.Type,
		Message: e.Message,
		X:       e.X,
		Y:       e.Y,
	}
	if e.CarID != "" {
		id := e.CarID
		out.CarID = &id
	}
	return out
}

// summaryFrom converts a save listing entry.
func summaryFrom(s session.Summary) *model.SessionSummary {
	return &model.SessionSummary{
		ID:         s.ID,
		CreatedAt:  s.CreatedAt,
		Tick:       int(s.Tick),
		Speed:      s.Speed,
		Money:      s.Money,
		Reputation: s.Reputation,
		Deliveries: s.Deliveries,
		Loaded:     s.Loaded,
	}
}

// --- enum translation ------------------------------------------------------

func carStateFrom(s string) model.CarState {
	switch s {
	case "carrying":
		return model.CarStateCarrying
	case "charging":
		return model.CarStateCharging
	case "stranded":
		return model.CarStateStranded
	}
	return model.CarStateIdle
}

func orderStateFrom(s string) model.OrderState {
	switch s {
	case "assigned":
		return model.OrderStateAssigned
	case "picked_up":
		return model.OrderStatePickedUp
	case "done":
		return model.OrderStateDone
	case "expired":
		return model.OrderStateExpired
	}
	return model.OrderStatePending
}

func rejectFrom(s string) (model.RejectReason, bool) {
	switch s {
	case "bad_direction":
		return model.RejectReasonBadDirection, true
	case "off_grid":
		return model.RejectReasonOffGrid, true
	case "blocked":
		return model.RejectReasonBlocked, true
	case "no_battery":
		return model.RejectReasonNoBattery, true
	}
	return "", false
}

func directionFrom(s string) (model.Direction, bool) {
	switch strings.ToLower(s) {
	case "north", "up":
		return model.DirectionNorth, true
	case "south", "down":
		return model.DirectionSouth, true
	case "east", "right":
		return model.DirectionEast, true
	case "west", "left":
		return model.DirectionWest, true
	}
	return "", false
}

// engineDirection turns a schema enum into the engine's vocabulary.
func engineDirection(d model.Direction) string {
	switch d {
	case model.DirectionNorth:
		return "north"
	case model.DirectionSouth:
		return "south"
	case model.DirectionEast:
		return "east"
	case model.DirectionWest:
		return "west"
	}
	return ""
}

// --- small helpers ---------------------------------------------------------

// optionalString maps "" to nil so absent data is null rather than empty text.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nonNil keeps list fields as [] rather than null, so clients can iterate
// without a nil check.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

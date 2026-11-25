package graph

import (
	"sort"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// Conversion from stored per-tick frames (game/session.TickSnapshot) to the
// GraphQL model. Frames are decoded engine.SavedState values, so this maps the
// same engine types the live snapshot conversion reads, but only the fields the
// frame schema exposes.

func tickSnapshotsFrom(rows []session.TickSnapshot) []*model.TickSnapshot {
	out := make([]*model.TickSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, tickSnapshotFrom(row))
	}
	return out
}

func tickSnapshotFrom(row session.TickSnapshot) *model.TickSnapshot {
	state := row.State
	out := &model.TickSnapshot{
		SessionID:        row.SessionID,
		Tick:             int(row.Tick),
		RecordedAt:       row.RecordedAt,
		DeliveriesByItem: make([]*model.ItemCount, 0),
		Cars:             make([]*model.TickCar, 0, len(state.Cars)),
		Orders:           make([]*model.TickOrder, 0, len(state.Orders)),
	}
	if ledger := state.Ledger; ledger != nil {
		out.Money = ledger.Money
		out.MoneyEarned = ledger.MoneyEarned
		out.Reputation = ledger.Reputation
		out.Deliveries = ledger.Deliveries
		out.LateDeliveries = ledger.LateDeliveries
		out.ExpiredOrders = ledger.ExpiredOrders
		out.OrdersSpawned = ledger.OrdersSpawned
		out.OnTimeStreak = ledger.OnTimeStreak
		out.Tows = ledger.Tows
		out.UpkeepPaid = ledger.UpkeepPaid
		out.TowFees = ledger.TowFees
		items := make([]string, 0, len(ledger.DeliveriesByItem))
		for item := range ledger.DeliveriesByItem {
			items = append(items, item)
		}
		sort.Strings(items)
		for _, item := range items {
			out.DeliveriesByItem = append(out.DeliveriesByItem, &model.ItemCount{
				Item: item, Count: ledger.DeliveriesByItem[item],
			})
		}
	}
	for _, c := range state.Cars {
		if c == nil {
			continue
		}
		car := &model.TickCar{
			ID:         c.ID,
			Name:       c.Name,
			ClassName:  c.ClassName,
			X:          c.Pos.X,
			Y:          c.Pos.Y,
			Battery:    c.Battery,
			Cargo:      append([]string(nil), c.Cargo...),
			ChargeDue:  c.ChargeDue,
			LastReject: string(c.LastReject),
			StepsTaken: c.StepsTaken,
		}
		if car.Cargo == nil {
			car.Cargo = []string{}
		}
		if dir, ok := directionFrom(string(c.QueuedMove)); ok {
			car.QueuedMove = &dir
		}
		out.Cars = append(out.Cars, car)
	}
	for _, o := range state.Orders {
		if o == nil {
			continue
		}
		out.Orders = append(out.Orders, &model.TickOrder{
			ID:            o.ID,
			Item:          o.Item,
			Quantity:      o.Quantity,
			KitchenID:     o.KitchenID,
			CustomerID:    o.CustomerID,
			AssignedCar:   o.AssignedCar,
			State:         orderStateFrom(string(o.State)),
			CreatedTick:   int(o.CreatedTick),
			DeadlineTick:  int(o.DeadlineTick),
			DeliveredTick: int(o.DeliveredTick),
			Reward:        o.Reward,
			Penalty:       o.Penalty,
			Paid:          o.Paid,
			Late:          o.Late,
		})
	}
	return out
}

package session

import (
	"context"
	"math"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// TestSaveProjectsHistoryIdempotently covers the write path: a save projects
// the summary, the new cash rows, and changed orders; saving the same state
// twice must not duplicate anything, and a terminal order must not regress if
// an older snapshot is projected later.
func TestSaveProjectsHistoryIdempotently(t *testing.T) {
	mgr := newTestManager(t)
	s, _, err := mgr.Create("history-1", 7, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s.Do(func(w *engine.World) {
		w.Ledger.Cash = []engine.CashEntry{
			{Seq: 0, Kind: engine.CashOpening, Amount: 50, Balance: 50, Memo: "opening balance"},
			{Seq: 1, Tick: 10, Kind: engine.CashDelivery, Amount: 100, Balance: 150},
		}
		w.Ledger.Money = 150
		w.Ledger.NextCashSeq = 2
		w.Orders = []*engine.Order{{
			ID: "order-1", Item: "box", Quantity: 2, KitchenID: "k1", CustomerID: "c1",
			CreatedTick: 5, DeadlineTick: 50, Reward: 100, State: engine.OrderPending,
		}}
	})
	if err := s.Save(); err != nil {
		t.Fatalf("first save: %v", err)
	}
	assertHistoryCounts(t, mgr, s.ID, 2, 1)
	assertSessionMoney(t, mgr, s.ID, 150)

	// Replaying the same snapshot must not duplicate cash or orders.
	if err := s.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	assertHistoryCounts(t, mgr, s.ID, 2, 1)

	s.Do(func(w *engine.World) {
		w.Ledger.Cash = append(w.Ledger.Cash,
			engine.CashEntry{Seq: 2, Tick: 20, Kind: engine.CashDelivery, Amount: 50, Balance: 200})
		w.Ledger.Money = 200
		w.Ledger.NextCashSeq = 3
		w.Orders[0].State = engine.OrderDone
		w.Orders[0].DeliveredTick = 20
		w.Orders[0].Paid = 100
	})
	if err := s.Save(); err != nil {
		t.Fatalf("third save: %v", err)
	}
	assertHistoryCounts(t, mgr, s.ID, 3, 1)
	assertSessionMoney(t, mgr, s.ID, 200)

	var state string
	var paid int
	if err := mgr.history.pool.QueryRow(context.Background(),
		`SELECT state, paid FROM delivery_order_history WHERE session_id = $1 AND order_id = 'order-1'`,
		s.ID,
	).Scan(&state, &paid); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if state != string(engine.OrderDone) || paid != 100 {
		t.Fatalf("order = %s/%d, want done/100", state, paid)
	}

	// A stale open snapshot must not regress a terminal row.
	if err := mgr.history.Project(context.Background(), historySnapshot{
		ID: s.ID, CityID: "city", CreatedAt: s.CreatedAt, Tick: 10, Money: 150,
		LastCashSeq: 1,
		Orders: []*engine.Order{{
			ID: "order-1", Item: "box", Quantity: 2, KitchenID: "k1", CustomerID: "c1",
			CreatedTick: 5, DeadlineTick: 50, Reward: 100, State: engine.OrderPending,
		}},
	}); err != nil {
		t.Fatalf("stale projection: %v", err)
	}
	if err := mgr.history.pool.QueryRow(context.Background(),
		`SELECT state FROM delivery_order_history WHERE session_id = $1 AND order_id = 'order-1'`,
		s.ID,
	).Scan(&state); err != nil {
		t.Fatalf("read order after stale projection: %v", err)
	}
	if state != string(engine.OrderDone) {
		t.Fatalf("state = %s, want done (stale snapshot regressed it)", state)
	}

	// Deleting the session removes its derived rows through the FK cascade.
	if err := mgr.Delete(s.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	assertHistoryCounts(t, mgr, s.ID, 0, 0)
	var sessionRows int
	if err := mgr.history.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_sessions WHERE id = $1`, s.ID,
	).Scan(&sessionRows); err != nil {
		t.Fatalf("count sessions after delete: %v", err)
	}
	if sessionRows != 0 {
		t.Fatalf("delivery_sessions rows after delete = %d, want 0", sessionRows)
	}
}

// TestBackfillHistoryRebuildsFromPayload deletes the projections and rebuilds
// them from platform_sessions, then confirms a second run is idempotent.
func TestBackfillHistoryRebuildsFromPayload(t *testing.T) {
	mgr := newTestManager(t)
	s, _, err := mgr.Create("history-backfill", 3, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Do(func(w *engine.World) {
		w.Ledger.Cash = []engine.CashEntry{
			{Seq: 0, Kind: engine.CashOpening, Amount: 25, Balance: 25},
			{Seq: 1, Tick: 4, Kind: engine.CashDelivery, Amount: 30, Balance: 55},
		}
		w.Ledger.Money = 55
		w.Ledger.NextCashSeq = 2
		w.Orders = []*engine.Order{{
			ID: "order-1", Item: "box", Quantity: 3, KitchenID: "k1", CustomerID: "c1",
			CreatedTick: 2, DeadlineTick: 40, Reward: 30, State: engine.OrderDone,
			DeliveredTick: 4, Paid: 30,
		}}
	})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	empty, _, err := mgr.Create("history-empty", 4, "", "city")
	if err != nil {
		t.Fatalf("create empty: %v", err)
	}
	if err := empty.Save(); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM delivery_order_history`,
		`DELETE FROM delivery_cash_entries`,
		`DELETE FROM delivery_sessions`,
	} {
		if _, err := mgr.history.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("clear projections: %v", err)
		}
	}

	written, err := BackfillHistory(ctx, mgr.history.pool, BackfillOptions{})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if written != 2 {
		t.Fatalf("backfilled %d sessions, want 2", written)
	}
	assertHistoryCounts(t, mgr, s.ID, 2, 1)
	assertHistoryCounts(t, mgr, empty.ID, 0, 0)

	if _, err := BackfillHistory(ctx, mgr.history.pool, BackfillOptions{}); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	assertHistoryCounts(t, mgr, s.ID, 2, 1)
	assertHistoryCounts(t, mgr, empty.ID, 0, 0)
}

func assertHistoryCounts(t *testing.T, mgr *Manager, id string, cash, orders int) {
	t.Helper()
	ctx := context.Background()
	var gotCash, gotOrders int
	if err := mgr.history.pool.QueryRow(ctx,
		`SELECT count(*) FROM delivery_cash_entries WHERE session_id = $1`, id,
	).Scan(&gotCash); err != nil {
		t.Fatalf("count cash: %v", err)
	}
	if err := mgr.history.pool.QueryRow(ctx,
		`SELECT count(*) FROM delivery_order_history WHERE session_id = $1`, id,
	).Scan(&gotOrders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if gotCash != cash || gotOrders != orders {
		t.Fatalf("history = %d cash/%d orders, want %d/%d", gotCash, gotOrders, cash, orders)
	}
}

func assertSessionMoney(t *testing.T, mgr *Manager, id string, money int) {
	t.Helper()
	var got int
	if err := mgr.history.pool.QueryRow(context.Background(),
		`SELECT money FROM delivery_sessions WHERE id = $1`, id,
	).Scan(&got); err != nil {
		t.Fatalf("read session money: %v", err)
	}
	if got != money {
		t.Fatalf("money = %d, want %d", got, money)
	}
}

func TestMetricHistoryMoneyReadsProjectionAndDownsamples(t *testing.T) {
	mgr := newTestManager(t)
	first, _, err := mgr.Create("money-a", 1, "", "city")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, _, err := mgr.Create("money-b", 2, "", "city")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	first.Do(func(w *engine.World) {
		w.Tick = 99
		w.Ledger.Cash = make([]engine.CashEntry, 0, 100)
		for i := 0; i < 100; i++ {
			w.Ledger.Cash = append(w.Ledger.Cash, engine.CashEntry{
				Seq: i, Tick: int64(i), Kind: engine.CashDelivery, Amount: 1, Balance: i,
			})
		}
		w.Ledger.Money = 99
		w.Ledger.NextCashSeq = 100
	})
	second.Do(func(w *engine.World) {
		w.Tick = 10
		w.Ledger.Cash = []engine.CashEntry{
			{Seq: 0, Tick: 0, Kind: engine.CashOpening, Amount: 5, Balance: 5},
			{Seq: 1, Tick: 10, Kind: engine.CashDelivery, Amount: 7, Balance: 12},
		}
		w.Ledger.Money = 12
		w.Ledger.NextCashSeq = 2
	})
	for _, s := range []*Session{first, second} {
		if err := s.Save(); err != nil {
			t.Fatalf("save %s: %v", s.ID, err)
		}
	}

	got, err := mgr.MetricHistory(context.Background(), MetricMoney, SeriesRequest{
		IDs:       []string{first.ID, second.ID},
		MaxPoints: 10,
	})
	if err != nil {
		t.Fatalf("money history: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("series = %d, want 2", len(got))
	}
	if got[0].SessionID != first.ID || got[1].SessionID != second.ID {
		t.Fatalf("order = %s/%s, want %s/%s", got[0].SessionID, got[1].SessionID, first.ID, second.ID)
	}
	if len(got[0].Points) > 12 {
		t.Fatalf("downsampled points = %d, want <= 12", len(got[0].Points))
	}
	if firstPoint, lastPoint := got[0].Points[0], got[0].Points[len(got[0].Points)-1]; firstPoint.Tick != 0 || lastPoint.Tick != 99 || lastPoint.Value != 99 {
		t.Fatalf("first/last = %+v/%+v, want tick 0 and tick 99 money 99", firstPoint, lastPoint)
	}
	if last := got[1].Points[len(got[1].Points)-1]; last.Tick != 10 || last.Value != 12 {
		t.Fatalf("second last = %+v, want tick 10 money 12", last)
	}
}

func TestMetricHistoryRejectsInvalidID(t *testing.T) {
	mgr := newTestManager(t)
	if _, err := mgr.MetricHistory(context.Background(), MetricMoney, SeriesRequest{IDs: []string{"bad/id"}}); err == nil {
		t.Fatal("MetricHistory accepted an invalid session id")
	}
}

func TestMetricHistoryBoxesIsCumulativeDoneBoxes(t *testing.T) {
	mgr := newTestManager(t)
	s, _, err := mgr.Create("items-history", 11, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Do(func(w *engine.World) {
		w.Tick = 40
		w.Orders = []*engine.Order{
			{ID: "o1", Item: "pizza", Quantity: 2, State: engine.OrderDone, DeliveredTick: 10, CreatedTick: 1, DeadlineTick: 9},
			{ID: "o2", Item: "cafe", Quantity: 3, State: engine.OrderDone, DeliveredTick: 20, CreatedTick: 2, DeadlineTick: 19},
			{ID: "o3", Item: "pizza", Quantity: 5, State: engine.OrderExpired, CreatedTick: 3, DeadlineTick: 18},
			{ID: "o4", Item: "pastel", Quantity: 1, State: engine.OrderDone, DeliveredTick: 30, CreatedTick: 4, DeadlineTick: 29},
			{ID: "o5", Item: "pizza", Quantity: 7, State: engine.OrderPending, CreatedTick: 5, DeadlineTick: 50},
		}
	})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err := mgr.MetricHistory(context.Background(), MetricBoxesDelivered, SeriesRequest{IDs: []string{s.ID}, MaxPoints: 50})
	if err != nil {
		t.Fatalf("items history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("series = %d, want 1", len(rows))
	}
	assertSeries(t, rows[0].Points, []SeriesPoint{
		{Tick: 0, Value: 0},
		{Tick: 10, Value: 2},
		{Tick: 20, Value: 5},
		{Tick: 30, Value: 6},
		{Tick: 40, Value: 6},
	})
}

func TestMetricHistoryReputationUsesRecordedEntries(t *testing.T) {
	mgr := newTestManager(t)
	s, _, err := mgr.Create("reputation-history", 12, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Do(func(w *engine.World) {
		w.Tick = 50
		w.Ledger.Reputation = 4.9
		w.Ledger.ReputationEntries = []engine.ReputationEntry{
			{Seq: 0, Tick: 0, Delta: 5, Balance: 5, Reason: engine.ReputationOpening},
			{Seq: 1, Tick: 50, Delta: -0.1, Balance: 4.9, Reason: engine.ReputationExpired, OrderID: "o1"},
		}
		w.Ledger.NextReputationSeq = 2
	})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err := mgr.MetricHistory(context.Background(), MetricReputation, SeriesRequest{IDs: []string{s.ID}, MaxPoints: 50})
	if err != nil {
		t.Fatalf("reputation history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("series = %d, want 1", len(rows))
	}
	assertSeries(t, rows[0].Points, []SeriesPoint{
		{Tick: 0, Value: 5},
		{Tick: 50, Value: 4.9},
	})
}

func TestMetricHistoryCumulativeSeries(t *testing.T) {
	mgr := newTestManager(t)
	s, _, err := mgr.Create("metric-history", 13, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Do(func(w *engine.World) {
		w.Tick = 40
		w.Orders = []*engine.Order{
			{ID: "o1", Item: "pizza", Quantity: 2, State: engine.OrderDone, CreatedTick: 1, DeadlineTick: 9, DeliveredTick: 10, Paid: 100},
			{ID: "o2", Item: "cafe", Quantity: 1, State: engine.OrderDone, CreatedTick: 2, DeadlineTick: 18, DeliveredTick: 20, Paid: 40, Late: true},
			{ID: "o3", Item: "pizza", Quantity: 3, State: engine.OrderExpired, CreatedTick: 3, DeadlineTick: 18, Penalty: 20},
			{ID: "o4", Item: "pastel", Quantity: 4, State: engine.OrderPending, CreatedTick: 5, DeadlineTick: 50},
		}
		w.Ledger.Cash = []engine.CashEntry{
			{Seq: 0, Tick: 0, Kind: engine.CashOpening, Amount: 100, Balance: 100},
			{Seq: 1, Tick: 12, Kind: engine.CashUpkeep, Amount: -5, Balance: 95},
			{Seq: 2, Tick: 19, Kind: engine.CashOrderFine, Amount: -15, Balance: 80},
			{Seq: 3, Tick: 22, Kind: engine.CashCharging, Amount: -3, Balance: 77},
			{Seq: 4, Tick: 33, Kind: engine.CashTow, Amount: -7, Balance: 70},
		}
		w.Ledger.NextCashSeq = 5
		w.Ledger.Money = 70
		w.Ledger.Reputation = 4.5
		w.Ledger.ReputationEntries = []engine.ReputationEntry{
			{Seq: 0, Tick: 0, Delta: 5, Balance: 5, Reason: engine.ReputationOpening},
			{Seq: 1, Tick: 25, Delta: -0.5, Balance: 4.5, Reason: engine.ReputationExpired},
		}
		w.Ledger.NextReputationSeq = 2
	})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	tests := []struct {
		metric Metric
		want   []SeriesPoint
	}{
		{MetricMoney, []SeriesPoint{{Tick: 0, Value: 100}, {Tick: 12, Value: 95}, {Tick: 19, Value: 80}, {Tick: 22, Value: 77}, {Tick: 33, Value: 70}, {Tick: 40, Value: 70}}},
		{MetricBoxesDelivered, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 10, Value: 2}, {Tick: 20, Value: 3}, {Tick: 40, Value: 3}}},
		{MetricReputation, []SeriesPoint{{Tick: 0, Value: 5}, {Tick: 25, Value: 4.5}, {Tick: 40, Value: 4.5}}},
		{MetricDeliveries, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 10, Value: 1}, {Tick: 40, Value: 1}}},
		{MetricLateDeliveries, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 20, Value: 1}, {Tick: 40, Value: 1}}},
		{MetricExpiredOrders, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 19, Value: 1}, {Tick: 40, Value: 1}}},
		{MetricOrdersCreated, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 1, Value: 1}, {Tick: 2, Value: 2}, {Tick: 3, Value: 3}, {Tick: 5, Value: 4}, {Tick: 40, Value: 4}}},
		{MetricDeliveryIncome, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 10, Value: 100}, {Tick: 20, Value: 140}, {Tick: 40, Value: 140}}},
		{MetricOrderFines, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 19, Value: 15}, {Tick: 40, Value: 15}}},
		{MetricUpkeepSpend, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 12, Value: 5}, {Tick: 40, Value: 5}}},
		{MetricChargingSpend, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 22, Value: 3}, {Tick: 40, Value: 3}}},
		{MetricTowSpend, []SeriesPoint{{Tick: 0, Value: 0}, {Tick: 33, Value: 7}, {Tick: 40, Value: 7}}},
	}
	for _, tt := range tests {
		rows, err := mgr.MetricHistory(context.Background(), tt.metric, SeriesRequest{IDs: []string{s.ID}, MaxPoints: 50})
		if err != nil {
			t.Fatalf("%s: %v", tt.metric, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: series = %d, want 1", tt.metric, len(rows))
		}
		assertSeries(t, rows[0].Points, tt.want)
	}

	if _, err := mgr.MetricHistory(context.Background(), Metric("nope"), SeriesRequest{IDs: []string{s.ID}}); err == nil {
		t.Fatal("MetricHistory accepted an unknown metric")
	}
}

func assertSeries(t *testing.T, got, want []SeriesPoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("points = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Tick != want[i].Tick || math.Abs(got[i].Value-want[i].Value) > 1e-9 {
			t.Fatalf("point %d = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

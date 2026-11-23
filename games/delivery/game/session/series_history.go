package session

import (
	"context"
	"fmt"
	"sort"
)

// SeriesRequest selects sessions and a tick window for analytics. It is the
// same shape for every metric so the chart controls are shared.
type SeriesRequest = MoneyHistoryRequest

// SeriesPoint is one downsampled value at a tick.
type SeriesPoint struct {
	Tick  int64
	Value float64
}

// SeriesHistory is one session's downsampled series.
type SeriesHistory struct {
	SessionID string
	CityID    string
	Points    []SeriesPoint
}

type seriesSummary struct {
	id         string
	city       string
	tick       int64
	money      int
	reputation float64
}

type seriesWindow struct {
	ordered    []seriesSummary
	sessionIDs []string
	lo         int64
	hi         int64
	width      int64
}

// prepareSeriesWindow loads the selected summaries and resolves the common
// tick window and bucket width. It is the shared front half of every metric
// reader; the metrics differ only in which table supplies the points.
func (m *Manager) prepareSeriesWindow(ctx context.Context, req SeriesRequest) (seriesWindow, error) {
	if m.history == nil {
		return seriesWindow{}, fmt.Errorf("series history: history store is unavailable")
	}
	ids, err := validHistoryIDs(req.IDs)
	if err != nil {
		return seriesWindow{}, err
	}
	maxPoints := req.MaxPoints
	if maxPoints <= 0 {
		maxPoints = 500
	}
	if maxPoints < 10 {
		maxPoints = 10
	}
	if maxPoints > maxMoneyHistoryPoints {
		maxPoints = maxMoneyHistoryPoints
	}

	var idsArg []string
	if len(ids) > 0 {
		idsArg = ids
	}
	limit := maxMoneyHistorySessions
	if req.Visible != nil {
		limit = maxVisibleHistoryScan
	}
	rows, err := m.history.pool.Query(ctx, `
SELECT id, city_id, tick, money, reputation
FROM delivery_sessions
WHERE ($1::text[] IS NULL OR id = ANY($1))
  AND ($2 = '' OR city_id = $2)
ORDER BY created_at DESC
LIMIT $3`, idsArg, req.CityID, limit)
	if err != nil {
		return seriesWindow{}, fmt.Errorf("series history: select sessions: %w", err)
	}
	var summaries []seriesSummary
	for rows.Next() {
		var s seriesSummary
		if err := rows.Scan(&s.id, &s.city, &s.tick, &s.money, &s.reputation); err != nil {
			rows.Close()
			return seriesWindow{}, fmt.Errorf("series history: scan session: %w", err)
		}
		if req.Visible != nil && !req.Visible(s.city) {
			continue
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return seriesWindow{}, fmt.Errorf("series history: select sessions: %w", err)
	}
	rows.Close()
	if len(summaries) > maxMoneyHistorySessions {
		summaries = summaries[:maxMoneyHistorySessions]
	}
	if len(summaries) == 0 {
		return seriesWindow{}, nil
	}

	ordered := summaries
	if len(ids) > 0 {
		byID := make(map[string]seriesSummary, len(summaries))
		for _, s := range summaries {
			byID[s.id] = s
		}
		ordered = make([]seriesSummary, 0, len(ids))
		for _, id := range ids {
			if s, ok := byID[id]; ok {
				ordered = append(ordered, s)
			}
		}
	}

	var hi int64
	for _, s := range ordered {
		if s.tick > hi {
			hi = s.tick
		}
	}
	lo := int64(0)
	if req.FromTick != nil {
		lo = *req.FromTick
		if lo < 0 {
			lo = 0
		}
	}
	if req.ToTick != nil && *req.ToTick < hi {
		hi = *req.ToTick
	}
	if hi < lo {
		return seriesWindow{ordered: nil}, nil
	}
	span := hi - lo
	width := int64(1)
	if span > int64(maxPoints) {
		width = (span + int64(maxPoints) - 1) / int64(maxPoints)
	}
	lo = (lo / width) * width

	sessionIDs := make([]string, 0, len(ordered))
	for _, s := range ordered {
		sessionIDs = append(sessionIDs, s.id)
	}
	return seriesWindow{ordered: ordered, sessionIDs: sessionIDs, lo: lo, hi: hi, width: width}, nil
}

// assembleSeries adds the left-edge anchor and the current summary point, then
// sorts and de-duplicates. fallback, when non-nil, is the value before any
// recorded movement (0 for cash and delivered items). Reputation passes nil
// because an old save has no knowable earlier value; its first point is the
// first recorded change.
func assembleSeries(
	ordered []seriesSummary,
	points map[string][]SeriesPoint,
	anchors map[string]float64,
	current map[string]float64,
	req SeriesRequest,
	lo int64,
	fallback *float64,
) []SeriesHistory {
	out := make([]SeriesHistory, 0, len(ordered))
	for _, s := range ordered {
		series := append([]SeriesPoint(nil), points[s.id]...)
		if anchor, ok := anchors[s.id]; ok {
			if len(series) == 0 || series[0].Tick > lo {
				series = append([]SeriesPoint{{Tick: lo, Value: anchor}}, series...)
			}
		} else if fallback != nil {
			if len(series) == 0 || series[0].Tick > lo {
				series = append([]SeriesPoint{{Tick: lo, Value: *fallback}}, series...)
			}
		}
		if req.ToTick == nil || *req.ToTick >= s.tick {
			value := current[s.id]
			if len(series) == 0 || series[len(series)-1].Tick < s.tick {
				series = append(series, SeriesPoint{Tick: s.tick, Value: value})
			}
		}
		sort.SliceStable(series, func(i, j int) bool { return series[i].Tick < series[j].Tick })
		deduped := series[:0]
		for _, p := range series {
			if n := len(deduped); n > 0 && deduped[n-1].Tick == p.Tick {
				deduped[n-1] = p
				continue
			}
			deduped = append(deduped, p)
		}
		out = append(out, SeriesHistory{SessionID: s.id, CityID: s.city, Points: deduped})
	}
	return out
}

// ItemsHistory is the cumulative number of boxes delivered at each tick,
// derived from done orders. Expired and open orders never contribute.
func (m *Manager) ItemsHistory(ctx context.Context, req SeriesRequest) ([]SeriesHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	w, err := m.prepareSeriesWindow(ctx, req)
	if err != nil || len(w.ordered) == 0 {
		return []SeriesHistory{}, err
	}

	anchors := map[string]float64{}
	rows, err := m.history.pool.Query(ctx, `
SELECT session_id, COALESCE(SUM(quantity), 0)
FROM delivery_order_history
WHERE session_id = ANY($1)
  AND state = 'done'
  AND delivered_tick < $2
GROUP BY session_id`, w.sessionIDs, w.lo)
	if err != nil {
		return nil, fmt.Errorf("items history: select anchors: %w", err)
	}
	for rows.Next() {
		var id string
		var items int64
		if err := rows.Scan(&id, &items); err != nil {
			rows.Close()
			return nil, fmt.Errorf("items history: scan anchor: %w", err)
		}
		anchors[id] = float64(items)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("items history: select anchors: %w", err)
	}
	rows.Close()

	// Collect per-bucket item counts first; the running total is applied after
	// the rows are grouped so each point is the cumulative delivery count.
	buckets := map[string]map[int64]float64{}
	order := map[string][]int64{}
	rows, err = m.history.pool.Query(ctx, `
SELECT session_id, delivered_tick, SUM(quantity)
FROM delivery_order_history
WHERE session_id = ANY($1)
  AND state = 'done'
  AND delivered_tick >= $2
  AND delivered_tick <= $3
GROUP BY session_id, delivered_tick
ORDER BY session_id, delivered_tick`, w.sessionIDs, w.lo, w.hi)
	if err != nil {
		return nil, fmt.Errorf("items history: select points: %w", err)
	}
	for rows.Next() {
		var id string
		var tick int64
		var items int64
		if err := rows.Scan(&id, &tick, &items); err != nil {
			rows.Close()
			return nil, fmt.Errorf("items history: scan point: %w", err)
		}
		bucket := (tick / w.width) * w.width
		if buckets[id] == nil {
			buckets[id] = map[int64]float64{}
		}
		if _, seen := buckets[id][bucket]; !seen {
			order[id] = append(order[id], bucket)
		}
		buckets[id][bucket] += float64(items)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("items history: select points: %w", err)
	}
	rows.Close()

	points := map[string][]SeriesPoint{}
	for id, ticks := range order {
		running := anchors[id]
		for _, tick := range ticks {
			running += buckets[id][tick]
			points[id] = append(points[id], SeriesPoint{Tick: tick, Value: running})
		}
	}

	current := map[string]float64{}
	rows, err = m.history.pool.Query(ctx, `
SELECT session_id, COALESCE(SUM(quantity), 0)
FROM delivery_order_history
WHERE session_id = ANY($1) AND state = 'done'
GROUP BY session_id`, w.sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("items history: select current: %w", err)
	}
	for rows.Next() {
		var id string
		var items int64
		if err := rows.Scan(&id, &items); err != nil {
			rows.Close()
			return nil, fmt.Errorf("items history: scan current: %w", err)
		}
		current[id] = float64(items)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("items history: select current: %w", err)
	}
	rows.Close()

	zero := 0.0
	return assembleSeries(w.ordered, points, anchors, current, req, w.lo, &zero), nil
}

// ReputationHistory is the recorded standing balance at each tick. Sessions
// saved before the reputation ledger existed return one current point until
// their next change.
func (m *Manager) ReputationHistory(ctx context.Context, req SeriesRequest) ([]SeriesHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	w, err := m.prepareSeriesWindow(ctx, req)
	if err != nil || len(w.ordered) == 0 {
		return []SeriesHistory{}, err
	}

	points := map[string][]SeriesPoint{}
	rows, err := m.history.pool.Query(ctx, `
SELECT session_id,
       (tick / $2) * $2 AS tick_bucket,
       (array_agg(balance ORDER BY tick DESC, seq DESC))[1] AS value
FROM delivery_reputation_entries
WHERE session_id = ANY($1)
  AND tick >= $3
  AND tick <= $4
GROUP BY session_id, (tick / $2) * $2
ORDER BY session_id, tick_bucket`, w.sessionIDs, w.width, w.lo, w.hi)
	if err != nil {
		return nil, fmt.Errorf("reputation history: select points: %w", err)
	}
	for rows.Next() {
		var id string
		var p SeriesPoint
		if err := rows.Scan(&id, &p.Tick, &p.Value); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reputation history: scan point: %w", err)
		}
		points[id] = append(points[id], p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("reputation history: select points: %w", err)
	}
	rows.Close()

	anchors := map[string]float64{}
	rows, err = m.history.pool.Query(ctx, `
SELECT DISTINCT ON (session_id) session_id, balance
FROM delivery_reputation_entries
WHERE session_id = ANY($1)
  AND tick <= $2
ORDER BY session_id, tick DESC, seq DESC`, w.sessionIDs, w.lo)
	if err != nil {
		return nil, fmt.Errorf("reputation history: select anchors: %w", err)
	}
	for rows.Next() {
		var id string
		var value float64
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reputation history: scan anchor: %w", err)
		}
		anchors[id] = value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("reputation history: select anchors: %w", err)
	}
	rows.Close()

	current := make(map[string]float64, len(w.ordered))
	for _, s := range w.ordered {
		current[s.id] = s.reputation
	}
	return assembleSeries(w.ordered, points, anchors, current, req, w.lo, nil), nil
}

// Metric identifies one cumulative operational series. Every metric counts
// events at a tick; spending metrics accumulate spend as a positive magnitude
// so one chart scale works for income and costs.
type Metric string

const (
	MetricDeliveries     Metric = "DELIVERIES"
	MetricLateDeliveries Metric = "LATE_DELIVERIES"
	MetricExpiredOrders  Metric = "EXPIRED_ORDERS"
	MetricOrdersCreated  Metric = "ORDERS_CREATED"
	MetricDeliveryIncome Metric = "DELIVERY_INCOME"
	MetricOrderFines     Metric = "ORDER_FINES"
	MetricUpkeepSpend    Metric = "UPKEEP_SPEND"
	MetricChargingSpend  Metric = "CHARGING_SPEND"
	MetricTowSpend       Metric = "TOW_SPEND"
)

// metricSpec is the SQL shape of one cumulative series: which projection
// table, the tick an event belongs to, the signed value it contributes, and
// the terminal-state filter. All strings are package constants, never input.
type metricSpec struct {
	table     string
	tickExpr  string
	valueExpr string
	where     string
}

var metricSpecs = map[Metric]metricSpec{
	MetricDeliveries: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "1::double precision", where: "state = 'done' AND NOT late",
	},
	MetricLateDeliveries: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "1::double precision", where: "state = 'done' AND late",
	},
	MetricExpiredOrders: {
		// Expiry lands on the first tick after the deadline.
		table: "delivery_order_history", tickExpr: "deadline_tick + 1",
		valueExpr: "1::double precision", where: "state = 'expired'",
	},
	MetricOrdersCreated: {
		table: "delivery_order_history", tickExpr: "created_tick",
		valueExpr: "1::double precision", where: "TRUE",
	},
	MetricDeliveryIncome: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "paid::double precision", where: "state = 'done'",
	},
	MetricOrderFines: {
		table: "delivery_order_history", tickExpr: "deadline_tick + 1",
		valueExpr: "penalty::double precision", where: "state = 'expired'",
	},
	MetricUpkeepSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'upkeep'",
	},
	MetricChargingSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'charging'",
	},
	MetricTowSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'tow'",
	},
}

// MetricHistory is the cumulative operational series selected by metric.
// Anchors before the window and the current total are read alongside the
// bucketed events, so a window starting mid-save still draws the right level.
func (m *Manager) MetricHistory(ctx context.Context, metric Metric, req SeriesRequest) ([]SeriesHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	spec, ok := metricSpecs[metric]
	if !ok {
		return nil, fmt.Errorf("metric history: unknown metric %q", metric)
	}
	w, err := m.prepareSeriesWindow(ctx, req)
	if err != nil || len(w.ordered) == 0 {
		return []SeriesHistory{}, err
	}

	points, anchors, current, err := m.cumulativeSeries(ctx, w, spec)
	if err != nil {
		return nil, err
	}
	zero := 0.0
	return assembleSeries(w.ordered, points, anchors, current, req, w.lo, &zero), nil
}

// cumulativeSeries reads the three queries every cumulative metric needs:
// totals before the window, per-bucket sums inside it, and the all-time total.
func (m *Manager) cumulativeSeries(ctx context.Context, w seriesWindow, spec metricSpec) (map[string][]SeriesPoint, map[string]float64, map[string]float64, error) {
	anchors := map[string]float64{}
	rows, err := m.history.pool.Query(ctx, fmt.Sprintf(`
SELECT session_id, COALESCE(SUM(%s), 0)::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) < $2
GROUP BY session_id`, spec.valueExpr, spec.table, spec.where, spec.tickExpr), w.sessionIDs, w.lo)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("metric history: select anchors: %w", err)
	}
	for rows.Next() {
		var id string
		var value float64
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("metric history: scan anchor: %w", err)
		}
		anchors[id] = value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, fmt.Errorf("metric history: select anchors: %w", err)
	}
	rows.Close()

	buckets := map[string]map[int64]float64{}
	order := map[string][]int64{}
	rows, err = m.history.pool.Query(ctx, fmt.Sprintf(`
SELECT session_id, ((%s) / $2) * $2 AS tick_bucket, SUM(%s)::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) >= $3
  AND (%s) <= $4
GROUP BY session_id, tick_bucket
ORDER BY session_id, tick_bucket`, spec.tickExpr, spec.valueExpr, spec.table, spec.where, spec.tickExpr, spec.tickExpr), w.sessionIDs, w.width, w.lo, w.hi)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("metric history: select points: %w", err)
	}
	for rows.Next() {
		var id string
		var tick int64
		var value float64
		if err := rows.Scan(&id, &tick, &value); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("metric history: scan point: %w", err)
		}
		if buckets[id] == nil {
			buckets[id] = map[int64]float64{}
		}
		if _, seen := buckets[id][tick]; !seen {
			order[id] = append(order[id], tick)
		}
		buckets[id][tick] += value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, fmt.Errorf("metric history: select points: %w", err)
	}
	rows.Close()

	points := map[string][]SeriesPoint{}
	for id, ticks := range order {
		running := anchors[id]
		for _, tick := range ticks {
			running += buckets[id][tick]
			points[id] = append(points[id], SeriesPoint{Tick: tick, Value: running})
		}
	}

	current := map[string]float64{}
	rows, err = m.history.pool.Query(ctx, fmt.Sprintf(`
SELECT session_id, COALESCE(SUM(%s), 0)::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
GROUP BY session_id`, spec.valueExpr, spec.table, spec.where), w.sessionIDs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("metric history: select current: %w", err)
	}
	for rows.Next() {
		var id string
		var value float64
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("metric history: scan current: %w", err)
		}
		current[id] = value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, fmt.Errorf("metric history: select current: %w", err)
	}
	rows.Close()
	return points, anchors, current, nil
}

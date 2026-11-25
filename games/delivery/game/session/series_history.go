package session

import (
	"context"
	"fmt"
	"sort"
)

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
	if maxPoints > maxHistoryPoints {
		maxPoints = maxHistoryPoints
	}

	var idsArg []string
	if len(ids) > 0 {
		idsArg = ids
	}
	limit := maxHistorySessions
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
	if len(summaries) > maxHistorySessions {
		summaries = summaries[:maxHistorySessions]
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

// Metric identifies one analytics series. Money, delivered boxes and
// reputation are the original three; the rest are cumulative operational
// counters. Keeping one enum means one request contract for every chart.
type Metric string

const (
	MetricMoney          Metric = "MONEY"
	MetricBoxesDelivered Metric = "BOXES_DELIVERED"
	MetricReputation     Metric = "REPUTATION"

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

// metricMode decides what a tick bucket means: a running total of events, or
// the last recorded value in the bucket (balances).
type metricMode int

const (
	metricCumulative metricMode = iota
	metricLatest
)

// metricSpec is the SQL shape of one series: which projection table, the tick
// an event belongs to, the value it contributes, terminal-state filter, and
// how buckets and anchors are aggregated. All strings are package constants,
// never input.
type metricSpec struct {
	table     string
	tickExpr  string
	valueExpr string
	where     string
	mode      metricMode
	// fallback is the value before the first event. Nil means the metric has
	// no knowable earlier value (a pre-ledger reputation save).
	fallback *float64
	// summaryValue, when set, supplies the current point from the session
	// summary instead of aggregating the projection.
	summaryValue func(seriesSummary) float64
}

var metricZero = 0.0

var metricSpecs = map[Metric]metricSpec{
	MetricMoney: {
		table: "delivery_cash_entries", tickExpr: "tick", valueExpr: "balance",
		where: "TRUE", mode: metricLatest, fallback: &metricZero,
		summaryValue: func(s seriesSummary) float64 { return float64(s.money) },
	},
	MetricBoxesDelivered: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "quantity::double precision", where: "state = 'done'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricReputation: {
		table: "delivery_reputation_entries", tickExpr: "tick", valueExpr: "balance",
		where: "TRUE", mode: metricLatest,
		summaryValue: func(s seriesSummary) float64 { return s.reputation },
	},
	MetricDeliveries: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "1::double precision", where: "state = 'done' AND NOT late",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricLateDeliveries: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "1::double precision", where: "state = 'done' AND late",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricExpiredOrders: {
		// Expiry lands on the first tick after the deadline.
		table: "delivery_order_history", tickExpr: "deadline_tick + 1",
		valueExpr: "1::double precision", where: "state = 'expired'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricOrdersCreated: {
		table: "delivery_order_history", tickExpr: "created_tick",
		valueExpr: "1::double precision", where: "TRUE",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricDeliveryIncome: {
		table: "delivery_order_history", tickExpr: "delivered_tick",
		valueExpr: "paid::double precision", where: "state = 'done'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricOrderFines: {
		// The cash ledger is authoritative: a fine is clamped to the balance,
		// so summing nominal penalties would overstate what was charged.
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'order_fine'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricUpkeepSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'upkeep'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricChargingSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'charging'",
		mode: metricCumulative, fallback: &metricZero,
	},
	MetricTowSpend: {
		table: "delivery_cash_entries", tickExpr: "tick",
		valueExpr: "(-amount)::double precision", where: "kind = 'tow'",
		mode: metricCumulative, fallback: &metricZero,
	},
}

// MetricHistory is the analytics series selected by metric. Balances
// (MONEY, REPUTATION) keep the last value in each bucket; the rest sum events
// into a running total. Anchors before the window and the current total are
// read alongside the bucketed events, so a window starting mid-save still
// draws the right level.
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

	points, anchors, current, err := m.metricSeries(ctx, w, spec)
	if err != nil {
		return nil, err
	}
	return assembleSeries(w.ordered, points, anchors, current, req, w.lo, spec.fallback), nil
}

// metricSeries reads the three queries every series needs: totals before the
// window, per-bucket aggregates inside it, and the current value.
func (m *Manager) metricSeries(ctx context.Context, w seriesWindow, spec metricSpec) (map[string][]SeriesPoint, map[string]float64, map[string]float64, error) {
	anchors := map[string]float64{}
	anchorSQL := fmt.Sprintf(`
SELECT session_id, COALESCE(SUM(%s), 0)::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) < $2
GROUP BY session_id`, spec.valueExpr, spec.table, spec.where, spec.tickExpr)
	if spec.mode == metricLatest {
		anchorSQL = fmt.Sprintf(`
SELECT DISTINCT ON (session_id) session_id, %s::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) <= $2
ORDER BY session_id, (%s) DESC, seq DESC`, spec.valueExpr, spec.table, spec.where, spec.tickExpr, spec.tickExpr)
	}
	rows, err := m.history.pool.Query(ctx, anchorSQL, w.sessionIDs, w.lo)
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
	pointSQL := fmt.Sprintf(`
SELECT session_id, ((%s) / $2) * $2 AS tick_bucket, SUM(%s)::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) >= $3
  AND (%s) <= $4
GROUP BY session_id, tick_bucket
ORDER BY session_id, tick_bucket`, spec.tickExpr, spec.valueExpr, spec.table, spec.where, spec.tickExpr, spec.tickExpr)
	if spec.mode == metricLatest {
		pointSQL = fmt.Sprintf(`
SELECT session_id, ((%s) / $2) * $2 AS tick_bucket,
       (array_agg(%s ORDER BY (%s) DESC, seq DESC))[1]::double precision
FROM %s
WHERE session_id = ANY($1)
  AND (%s)
  AND (%s) >= $3
  AND (%s) <= $4
GROUP BY session_id, tick_bucket
ORDER BY session_id, tick_bucket`, spec.tickExpr, spec.valueExpr, spec.tickExpr, spec.table, spec.where, spec.tickExpr, spec.tickExpr)
	}
	rows, err = m.history.pool.Query(ctx, pointSQL, w.sessionIDs, w.width, w.lo, w.hi)
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
		if spec.mode == metricLatest {
			for _, tick := range ticks {
				points[id] = append(points[id], SeriesPoint{Tick: tick, Value: buckets[id][tick]})
			}
			continue
		}
		running := anchors[id]
		for _, tick := range ticks {
			running += buckets[id][tick]
			points[id] = append(points[id], SeriesPoint{Tick: tick, Value: running})
		}
	}

	current := map[string]float64{}
	if spec.summaryValue != nil {
		for _, s := range w.ordered {
			current[s.id] = spec.summaryValue(s)
		}
		return points, anchors, current, nil
	}

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

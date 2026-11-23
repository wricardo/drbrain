package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// historyTimeout bounds one projection transaction. Saves already have a
// store timeout; history is derived data, so a slow projection must not hold a
// save open indefinitely.
const historyTimeout = 10 * time.Second

// historyWriter projects the parts of a session that outlive the snapshot:
// the current summary, the append-only cash ledger, and order history.
//
// The payload in platform_sessions remains authoritative. Every write here is
// idempotent and can be replayed from the payload, which is what makes the
// projection safe to retry and rebuild.
type historyWriter struct {
	pool *pgxpool.Pool
}

func newHistoryWriter(pool *pgxpool.Pool) *historyWriter {
	return &historyWriter{pool: pool}
}

type historySnapshot struct {
	ID         string
	CityID     string
	AccountID  string
	CreatedAt  time.Time
	Tick       int64
	Money      int
	Reputation float64

	// Cash is the batch to insert. Entries already projected are omitted.
	Cash []engine.CashEntry
	// LastCashSeq is the highest seq in the full ledger, not just Cash.
	LastCashSeq int
	// ReputationEntries is the batch of standing entries to insert.
	ReputationEntries []engine.ReputationEntry
	// LastReputationSeq is the highest seq in the full history.
	LastReputationSeq int
	// Orders is the batch to upsert: new or changed since the last projection.
	Orders []*engine.Order
}

func (h *historyWriter) Project(ctx context.Context, s historySnapshot) error {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("history: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO delivery_sessions
    (id, city_id, account_id, created_at, tick, money, reputation, last_cash_seq, last_reputation_seq, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (id) DO UPDATE SET
    city_id       = EXCLUDED.city_id,
    account_id    = EXCLUDED.account_id,
    tick          = GREATEST(delivery_sessions.tick, EXCLUDED.tick),
    money         = CASE WHEN EXCLUDED.tick >= delivery_sessions.tick
                         THEN EXCLUDED.money ELSE delivery_sessions.money END,
    reputation    = CASE WHEN EXCLUDED.tick >= delivery_sessions.tick
                         THEN EXCLUDED.reputation ELSE delivery_sessions.reputation END,
    last_cash_seq = GREATEST(delivery_sessions.last_cash_seq, EXCLUDED.last_cash_seq),
    last_reputation_seq = GREATEST(delivery_sessions.last_reputation_seq, EXCLUDED.last_reputation_seq),
    updated_at    = now()`,
		s.ID, s.CityID, s.AccountID, s.CreatedAt, s.Tick, s.Money, s.Reputation,
		s.LastCashSeq, s.LastReputationSeq,
	); err != nil {
		return fmt.Errorf("history: upsert session %s: %w", s.ID, err)
	}

	if len(s.Cash) > 0 {
		batch := &pgx.Batch{}
		for _, e := range s.Cash {
			batch.Queue(`
INSERT INTO delivery_cash_entries
    (session_id, seq, tick, kind, amount, balance, car_id, car_name, order_id, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (session_id, seq) DO NOTHING`,
				s.ID, e.Seq, e.Tick, string(e.Kind), e.Amount, e.Balance,
				e.CarID, e.CarName, e.OrderID, e.Memo,
			)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("history: insert cash for %s: %w", s.ID, err)
		}
	}

	if len(s.ReputationEntries) > 0 {
		batch := &pgx.Batch{}
		for _, e := range s.ReputationEntries {
			batch.Queue(`
INSERT INTO delivery_reputation_entries
    (session_id, seq, tick, delta, balance, reason, order_id, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (session_id, seq) DO NOTHING`,
				s.ID, e.Seq, e.Tick, e.Delta, e.Balance, string(e.Reason), e.OrderID, e.Memo,
			)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("history: insert reputation for %s: %w", s.ID, err)
		}
	}

	if len(s.Orders) > 0 {
		batch := &pgx.Batch{}
		for _, o := range s.Orders {
			batch.Queue(`
INSERT INTO delivery_order_history
    (session_id, order_id, item, quantity, kitchen_id, customer_id, assigned_car,
     state, created_tick, deadline_tick, delivered_tick, reward, penalty, paid, late, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
ON CONFLICT (session_id, order_id) DO UPDATE SET
    item           = EXCLUDED.item,
    quantity       = EXCLUDED.quantity,
    kitchen_id     = EXCLUDED.kitchen_id,
    customer_id    = EXCLUDED.customer_id,
    assigned_car   = EXCLUDED.assigned_car,
    state          = EXCLUDED.state,
    created_tick   = EXCLUDED.created_tick,
    deadline_tick  = EXCLUDED.deadline_tick,
    delivered_tick = EXCLUDED.delivered_tick,
    reward         = EXCLUDED.reward,
    penalty        = EXCLUDED.penalty,
    paid           = EXCLUDED.paid,
    late           = EXCLUDED.late,
    updated_at     = now()
WHERE delivery_order_history.state NOT IN ('done', 'expired')
   OR EXCLUDED.state = delivery_order_history.state`,
				s.ID, o.ID, o.Item, o.Boxes(), o.KitchenID, o.CustomerID, o.AssignedCar,
				string(o.State), o.CreatedTick, o.DeadlineTick, o.DeliveredTick,
				o.Reward, o.Penalty, o.Paid, o.Late,
			)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("history: upsert orders for %s: %w", s.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("history: commit %s: %w", s.ID, err)
	}
	return nil
}

// loadState seeds a session's in-memory projection watermark after it is
// loaded or created. The cash watermark is the max seq already stored, so a
// reload does not re-send the whole ledger. Order signatures are read back so
// only orders that actually changed are upserted.
func (h *historyWriter) loadState(ctx context.Context, id string) (int, int, map[string]orderSignature, error) {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()

	var cashSeq int
	if err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), -1) FROM delivery_cash_entries WHERE session_id = $1`,
		id,
	).Scan(&cashSeq); err != nil {
		return 0, 0, nil, fmt.Errorf("history: cash watermark %s: %w", id, err)
	}

	var reputationSeq int
	if err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), -1) FROM delivery_reputation_entries WHERE session_id = $1`,
		id,
	).Scan(&reputationSeq); err != nil {
		return 0, 0, nil, fmt.Errorf("history: reputation watermark %s: %w", id, err)
	}

	rows, err := h.pool.Query(ctx, `
SELECT order_id, item, quantity, kitchen_id, customer_id, assigned_car, state,
       created_tick, deadline_tick, delivered_tick, reward, penalty, paid, late
FROM delivery_order_history
WHERE session_id = $1`, id)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("history: read orders %s: %w", id, err)
	}
	defer rows.Close()

	sigs := map[string]orderSignature{}
	for rows.Next() {
		var orderID string
		var sig orderSignature
		if err := rows.Scan(
			&orderID, &sig.Item, &sig.Quantity, &sig.KitchenID, &sig.CustomerID,
			&sig.AssignedCar, &sig.State, &sig.CreatedTick, &sig.DeadlineTick,
			&sig.DeliveredTick, &sig.Reward, &sig.Penalty, &sig.Paid, &sig.Late,
		); err != nil {
			return 0, 0, nil, fmt.Errorf("history: scan order %s: %w", id, err)
		}
		sigs[orderID] = sig
	}
	if err := rows.Err(); err != nil {
		return 0, 0, nil, fmt.Errorf("history: read orders %s: %w", id, err)
	}
	return cashSeq, reputationSeq, sigs, nil
}

// orderSignature is the comparable projection of one Order. It includes every
// field the history table stores, so a change to any of them triggers an
// upsert.
type orderSignature struct {
	Item          string
	Quantity      int
	KitchenID     string
	CustomerID    string
	AssignedCar   string
	State         string
	CreatedTick   int64
	DeadlineTick  int64
	DeliveredTick int64
	Reward        int
	Penalty       int
	Paid          int
	Late          bool
}

func orderSignatureOf(o *engine.Order) orderSignature {
	return orderSignature{
		Item:          o.Item,
		Quantity:      o.Boxes(),
		KitchenID:     o.KitchenID,
		CustomerID:    o.CustomerID,
		AssignedCar:   o.AssignedCar,
		State:         string(o.State),
		CreatedTick:   o.CreatedTick,
		DeadlineTick:  o.DeadlineTick,
		DeliveredTick: o.DeliveredTick,
		Reward:        o.Reward,
		Penalty:       o.Penalty,
		Paid:          o.Paid,
		Late:          o.Late,
	}
}

// projectHistory writes the derived tables after a successful session save.
// It is deliberately best-effort: a projection failure must not turn a
// successful save into an error the player sees. The next save retries from
// the last committed watermark.
func (s *Session) projectHistory(data persisted) {
	if s.history == nil || data.World.Ledger == nil {
		return
	}
	ctx := context.Background()
	if !s.historyReady {
		cashSeq, reputationSeq, sigs, err := s.history.loadState(ctx, s.ID)
		if err != nil {
			slog.Default().Warn("delivery history: load watermark", "session", s.ID, "err", err)
			return
		}
		s.historyCashSeq = cashSeq
		s.historyReputationSeq = reputationSeq
		s.historyOrders = sigs
		s.historyReady = true
	}

	fullCash := data.World.Ledger.Cash
	lastSeq := s.historyCashSeq
	newCash := make([]engine.CashEntry, 0, len(fullCash))
	for _, e := range fullCash {
		if e.Seq > lastSeq {
			lastSeq = e.Seq
		}
		if e.Seq > s.historyCashSeq {
			newCash = append(newCash, e)
		}
	}

	fullReputation := data.World.Ledger.ReputationEntries
	lastReputationSeq := s.historyReputationSeq
	newReputation := make([]engine.ReputationEntry, 0, len(fullReputation))
	for _, e := range fullReputation {
		if e.Seq > lastReputationSeq {
			lastReputationSeq = e.Seq
		}
		if e.Seq > s.historyReputationSeq {
			newReputation = append(newReputation, e)
		}
	}

	orders := make([]*engine.Order, 0, len(data.World.Orders))
	for _, o := range data.World.Orders {
		if o == nil {
			continue
		}
		sig := orderSignatureOf(o)
		if existing, ok := s.historyOrders[o.ID]; !ok || existing != sig {
			orders = append(orders, o)
		}
	}

	snapshot := historySnapshot{
		ID:                s.ID,
		CityID:            s.cityID,
		AccountID:         s.accountID,
		CreatedAt:         s.CreatedAt,
		Tick:              data.World.Tick,
		Money:             data.World.Ledger.Money,
		Reputation:        data.World.Ledger.Reputation,
		Cash:              newCash,
		LastCashSeq:       lastSeq,
		ReputationEntries: newReputation,
		LastReputationSeq: lastReputationSeq,
		Orders:            orders,
	}
	if err := s.history.Project(ctx, snapshot); err != nil {
		slog.Default().Warn("delivery history: project", "session", s.ID, "err", err)
		return
	}

	s.historyCashSeq = lastSeq
	s.historyReputationSeq = lastReputationSeq
	if len(orders) > 0 && s.historyOrders == nil {
		s.historyOrders = map[string]orderSignature{}
	}
	for _, o := range orders {
		s.historyOrders[o.ID] = orderSignatureOf(o)
	}
}

// BackfillOptions controls BackfillHistory.
type BackfillOptions struct {
	// DryRun decodes and validates every delivery save without writing.
	DryRun bool
}

const (
	// MaxHistorySessions bounds one analytics read to at most this many
	// sessions; the GraphQL layer validates it before calling.
	MaxHistorySessions = 20
	// maxMoneyHistorySessions bounds one analytics query so a client cannot
	// ask for an unbounded overlay.
	maxMoneyHistorySessions = MaxHistorySessions
	// maxVisibleHistoryScan is the row budget when visibility filtering is on:
	// extra rows are scanned so hidden draft sessions cannot starve the
	// displayed overlay, which stays capped at maxMoneyHistorySessions.
	maxVisibleHistoryScan = maxMoneyHistorySessions * 10
	// maxMoneyHistoryPoints bounds one session's downsampled series.
	maxMoneyHistoryPoints = 2000
)

// MoneyHistoryRequest selects sessions and a tick window for analytics.
// FromTick and ToTick are pointers so an explicit zero is distinguishable from
// "unset".
type MoneyHistoryRequest struct {
	IDs       []string
	CityID    string
	FromTick  *int64
	ToTick    *int64
	MaxPoints int
	// Visible, when set, drops sessions whose city the caller may not read
	// (another player's draft). The query reads extra rows before filtering, so
	// a hidden session cannot empty the default newest-20 window.
	Visible func(cityID string) bool
}

// MoneyHistory is one session's downsampled money-by-tick series.
type MoneyHistory struct {
	SessionID string
	CityID    string
	Points    []MoneyPoint
}

// MoneyPoint is one balance at a tick.
type MoneyPoint struct {
	Tick  int64
	Money int
}

// MoneyHistory reads the indexed projection, never the session payloads or
// live session tickers. Points are bucketed across the requested window, and
// each bucket keeps the last balance in it; the current balance is appended
// when the window reaches the session's current tick.
func (m *Manager) MoneyHistory(ctx context.Context, req MoneyHistoryRequest) ([]MoneyHistory, error) {
	if m.history == nil {
		return nil, fmt.Errorf("money history: history store is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()

	ids, err := validHistoryIDs(req.IDs)
	if err != nil {
		return nil, err
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
SELECT id, city_id, tick, money
FROM delivery_sessions
WHERE ($1::text[] IS NULL OR id = ANY($1))
  AND ($2 = '' OR city_id = $2)
ORDER BY created_at DESC
LIMIT $3`, idsArg, req.CityID, limit)
	if err != nil {
		return nil, fmt.Errorf("money history: select sessions: %w", err)
	}
	type summary struct {
		id    string
		city  string
		tick  int64
		money int
	}
	var summaries []summary
	for rows.Next() {
		var s summary
		if err := rows.Scan(&s.id, &s.city, &s.tick, &s.money); err != nil {
			rows.Close()
			return nil, fmt.Errorf("money history: scan session: %w", err)
		}
		if req.Visible != nil && !req.Visible(s.city) {
			continue
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("money history: select sessions: %w", err)
	}
	rows.Close()
	if len(summaries) > maxMoneyHistorySessions {
		summaries = summaries[:maxMoneyHistorySessions]
	}
	if len(summaries) == 0 {
		return []MoneyHistory{}, nil
	}

	// Preserve the caller's order when ids are explicit; otherwise keep the
	// newest-created order from the query.
	ordered := summaries
	if len(ids) > 0 {
		byID := make(map[string]summary, len(summaries))
		for _, s := range summaries {
			byID[s.id] = s
		}
		ordered = make([]summary, 0, len(ids))
		for _, id := range ids {
			if s, ok := byID[id]; ok {
				ordered = append(ordered, s)
			}
		}
		if len(ordered) == 0 {
			return []MoneyHistory{}, nil
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
		return []MoneyHistory{}, nil
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
	points := map[string][]MoneyPoint{}
	rows, err = m.history.pool.Query(ctx, `
SELECT session_id,
       (tick / $2) * $2 AS tick_bucket,
       (array_agg(balance ORDER BY tick DESC, seq DESC))[1] AS money
FROM delivery_cash_entries
WHERE session_id = ANY($1)
  AND tick >= $3
  AND tick <= $4
GROUP BY session_id, (tick / $2) * $2
ORDER BY session_id, tick_bucket`, sessionIDs, width, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("money history: select points: %w", err)
	}
	for rows.Next() {
		var id string
		var p MoneyPoint
		if err := rows.Scan(&id, &p.Tick, &p.Money); err != nil {
			rows.Close()
			return nil, fmt.Errorf("money history: scan point: %w", err)
		}
		points[id] = append(points[id], p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("money history: select points: %w", err)
	}
	rows.Close()

	anchors := map[string]int{}
	rows, err = m.history.pool.Query(ctx, `
SELECT DISTINCT ON (session_id) session_id, balance
FROM delivery_cash_entries
WHERE session_id = ANY($1)
  AND tick <= $2
ORDER BY session_id, tick DESC, seq DESC`, sessionIDs, lo)
	if err != nil {
		return nil, fmt.Errorf("money history: select anchors: %w", err)
	}
	for rows.Next() {
		var id string
		var money int
		if err := rows.Scan(&id, &money); err != nil {
			rows.Close()
			return nil, fmt.Errorf("money history: scan anchor: %w", err)
		}
		anchors[id] = money
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("money history: select anchors: %w", err)
	}
	rows.Close()

	out := make([]MoneyHistory, 0, len(ordered))
	for _, s := range ordered {
		series := points[s.id]
		if series == nil {
			series = []MoneyPoint{}
		}
		if anchor, ok := anchors[s.id]; ok {
			if len(series) == 0 || series[0].Tick > lo {
				series = append([]MoneyPoint{{Tick: lo, Money: anchor}}, series...)
			}
		} else if len(series) == 0 || series[0].Tick > lo {
			// No cash movement had happened by lo, so the balance was zero.
			series = append([]MoneyPoint{{Tick: lo, Money: 0}}, series...)
		}
		if req.ToTick == nil || *req.ToTick >= s.tick {
			if len(series) == 0 || series[len(series)-1].Tick < s.tick {
				series = append(series, MoneyPoint{Tick: s.tick, Money: s.money})
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
		out = append(out, MoneyHistory{SessionID: s.id, CityID: s.city, Points: deduped})
	}
	return out, nil
}

func validHistoryIDs(ids []string) ([]string, error) {
	if len(ids) > maxMoneyHistorySessions {
		return nil, fmt.Errorf("money history: at most %d sessions per query", maxMoneyHistorySessions)
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !ValidID(id) {
			return nil, fmt.Errorf("money history: invalid session id %q", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// BackfillHistory rebuilds the delivery projections from the authoritative
// platform_sessions payloads. It is safe to run repeatedly: cash inserts are
// idempotent and order upserts never regress a terminal state.
func BackfillHistory(ctx context.Context, pool *pgxpool.Pool, opts BackfillOptions) (int, error) {
	rows, err := pool.Query(ctx, `
SELECT id
FROM platform_sessions
WHERE convert_from(payload, 'UTF8')::jsonb ? 'city_id'
   OR convert_from(payload, 'UTF8')::jsonb ? 'city_file'
ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("backfill: list delivery sessions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("backfill: scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("backfill: list delivery sessions: %w", err)
	}
	rows.Close()

	writer := newHistoryWriter(pool)
	var written int
	var errs []error
	for _, id := range ids {
		var payload []byte
		if err := pool.QueryRow(ctx,
			`SELECT payload FROM platform_sessions WHERE id = $1`, id,
		).Scan(&payload); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: read payload: %w", id, err))
			continue
		}
		var p persisted
		if err := json.Unmarshal(payload, &p); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: decode: %w", id, err))
			continue
		}
		if p.World.Ledger == nil {
			continue
		}

		cityID := p.CityID
		if cityID == "" {
			cityID = legacyCityFileBase(p.CityFile)
		}
		lastSeq := -1
		for _, e := range p.World.Ledger.Cash {
			if e.Seq > lastSeq {
				lastSeq = e.Seq
			}
		}
		lastReputationSeq := -1
		for _, e := range p.World.Ledger.ReputationEntries {
			if e.Seq > lastReputationSeq {
				lastReputationSeq = e.Seq
			}
		}
		if opts.DryRun {
			written++
			continue
		}
		if err := writer.Project(ctx, historySnapshot{
			ID:                id,
			CityID:            cityID,
			AccountID:         sanitizeAccountID(p.AccountID),
			CreatedAt:         p.CreatedAt,
			Tick:              p.World.Tick,
			Money:             p.World.Ledger.Money,
			Reputation:        p.World.Ledger.Reputation,
			Cash:              p.World.Ledger.Cash,
			LastCashSeq:       lastSeq,
			ReputationEntries: p.World.Ledger.ReputationEntries,
			LastReputationSeq: lastReputationSeq,
			Orders:            p.World.Orders,
		}); err != nil {
			errs = append(errs, fmt.Errorf("backfill %s: %w", id, err))
			continue
		}
		written++
	}
	return written, errors.Join(errs...)
}

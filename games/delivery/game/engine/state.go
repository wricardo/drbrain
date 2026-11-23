package engine

// Save state.
//
// A save is (city file, SavedState). The city file stays the terrain truth and
// SavedState carries everything the session changed on top of it, so a map
// content fix still applies to an existing save instead of freezing old
// geography into it.
//
// SavedState is exported and imported as one struct rather than through narrow
// setters, because the narrow-setter shape had a real failure mode: a field the
// restore path forgot was silently left at its zero value. Build-added chargers,
// district reveals, achievement flags and the order spawner's memory were all
// lost across a reload while every individual setter looked correct.
//
// City mutations are NOT stored a second time. ImportState replays every growth
// stage due by the saved tick, so there is exactly one description of what a
// stage does: the city config.

// legacyEventMoved is the event type movement used to be stored under. It is no
// longer emitted; ImportState drops it from saves written before that change.
const legacyEventMoved = "car_moved"

// SavedState is the restartable half of a World: everything that is not derived
// from the city file.
type SavedState struct {
	// Tick is the number of completed ticks; a restore resumes at this tick.
	Tick int64 `json:"tick"`
	// Seed drives every deterministic decision, so a save without it cannot
	// replay.
	Seed uint64 `json:"seed"`
	// Ledger is the cash, reputation and achievement state.
	Ledger *Ledger `json:"ledger"`

	// Cars and Orders are deep copies taken by ExportState, so serialising them
	// does not race the ticker.
	Cars   []*Car   `json:"cars"`
	Orders []*Order `json:"orders"`

	// NextCarSeq and OrderSeq keep generated ids unique after a restore.
	NextCarSeq int `json:"next_car_seq"`
	OrderSeq   int `json:"order_seq"`

	// Growth is deliberately NOT stored: stage application is a pure function
	// of the tick, so ImportState replays every stage due by the saved tick and
	// derives districts, roads, licensed classes and chargers from the content.

	// Order spawner memory. Without it a reload looks like a fresh city and
	// re-raises the forced first order.
	NextEligibleTick map[string]int64 `json:"next_eligible_tick"`
	LastSpawnTick    int64            `json:"last_spawn_tick"`
	HasSpawned       bool             `json:"has_spawned"`

	// Events is the retained feed, newest last.
	Events []Event `json:"events"`
}

// ExportState returns a deep copy of the restartable world state, so a caller
// can serialise it without holding a lock and without the tick loop mutating
// what is being written.
func (w *World) ExportState() SavedState {
	ledger := w.Ledger.Clone()
	s := SavedState{
		Tick:             w.Tick,
		Seed:             w.Seed,
		Ledger:           &ledger,
		Cars:             make([]*Car, len(w.Cars)),
		Orders:           make([]*Order, len(w.Orders)),
		NextCarSeq:       w.NextCarSeq,
		OrderSeq:         w.orderSeq,
		NextEligibleTick: make(map[string]int64, len(w.spawn.NextEligibleTick)),
		LastSpawnTick:    w.spawn.LastSpawnTick,
		HasSpawned:       w.spawn.HasSpawned,
		Events:           append([]Event(nil), w.Events...),
	}
	for i, c := range w.Cars {
		s.Cars[i] = c.Clone()
	}
	for i, o := range w.Orders {
		cp := *o
		s.Orders[i] = &cp
	}
	for k, v := range w.spawn.NextEligibleTick {
		s.NextEligibleTick[k] = v
	}
	return s
}

// ImportState restores the mutable half of a world from a save. The city is
// NOT part of SavedState: the caller must build it from the same city file
// first, because growth, terrain and fog are reconstructed from that content.
//
// Most restored state is derived rather than trusted: growth stages due by
// s.Tick are replayed, closure windows are re-derived from (Seed, Tick) and the
// current fog, Ledger.Months is rebuilt from Cash, each car's box load is
// recomputed from its cargo, and NextCashSeq is backfilled for saves written
// before the cash ledger existed. The error return is currently always nil.
func (w *World) ImportState(s SavedState) error {
	if s.Ledger != nil {
		w.Ledger = s.Ledger
	}
	if w.Ledger.DeliveriesByItem == nil {
		w.Ledger.DeliveriesByItem = map[string]int{}
	}
	// Saves written before the cash ledger existed have no NextCashSeq. Start
	// at 1 and step past any seq already present so a restored session can
	// never hand out a duplicate entry id.
	if w.Ledger.NextCashSeq < 1 {
		w.Ledger.NextCashSeq = 1
	}
	for _, e := range w.Ledger.Cash {
		if e.Seq >= w.Ledger.NextCashSeq {
			w.Ledger.NextCashSeq = e.Seq + 1
		}
	}
	// Saves written before the reputation ledger existed have no
	// NextReputationSeq. Start at 1 and step past any seq already present.
	if w.Ledger.NextReputationSeq < 1 {
		w.Ledger.NextReputationSeq = 1
	}
	for _, e := range w.Ledger.ReputationEntries {
		if e.Seq >= w.Ledger.NextReputationSeq {
			w.Ledger.NextReputationSeq = e.Seq + 1
		}
	}
	// Monthly summaries are derived, not saved. Rebuilding from Cash also
	// backfills sessions written before the report existed.
	w.Ledger.rebuildCashMonths(w.ticksPerMonth)
	if s.Cars != nil {
		w.Cars = s.Cars
	}
	if s.Orders != nil {
		w.Orders = s.Orders
	}

	w.Tick = s.Tick
	w.NextCarSeq = s.NextCarSeq
	w.orderSeq = s.OrderSeq

	// The city is rebuilt, not restored: growth is a pure function of the tick,
	// so replaying every stage due by this tick reproduces the same districts,
	// roads, chargers and licensed classes. Events produced by the replay are
	// overwritten when the saved log is restored below.
	w.growthApplied = 0
	w.buyableClasses = map[string]bool{}
	w.applyGrowth()

	w.spawn.NextEligibleTick = orEmptyInt64(s.NextEligibleTick)
	w.spawn.LastSpawnTick = s.LastSpawnTick
	w.spawn.HasSpawned = s.HasSpawned

	// Rebuild each car's box load from its cargo and the orders' sizes.
	// cargoBoxes is unexported and therefore not serialised; recomputing it
	// here is what keeps it derived state rather than a second source of truth
	// that a reload could silently disagree with.
	for _, c := range w.Cars {
		// A queue is a head plus a tail; a save can only be written between
		// ticks, so an empty head with a non-empty tail means the file was
		// hand-edited. Promote rather than run a route with no head.
		if c.QueuedMove == "" && len(c.MoveQueue) > 0 {
			c.QueuedMove = c.MoveQueue[0]
			c.MoveQueue = c.MoveQueue[1:]
		}
		if len(c.MoveQueue) > MaxQueuedMoves-1 {
			c.MoveQueue = c.MoveQueue[:MaxQueuedMoves-1]
		}

		c.cargoBoxes = 0
		for _, id := range c.Cargo {
			if o, ok := w.Order(id); ok {
				c.cargoBoxes += o.Boxes()
			}
		}
	}

	if s.Events != nil {
		// Movement used to enter the feed, one entry per car per tick. It
		// drowned the events a player can act on and is redundant with the car
		// positions, so it is no longer emitted; drop the rows older saves
		// still carry rather than displaying them forever.
		kept := make([]Event, 0, len(s.Events))
		for _, e := range s.Events {
			if e.Type == legacyEventMoved {
				continue
			}
			kept = append(kept, e)
		}
		w.Events = kept
	}

	// Street works are derived from (seed, tick, chapter) rather than stored, so
	// the in-memory cache has to be rebuilt for the restored tick. Skipping this
	// made a save taken during road works reload with the street open, and the
	// reloaded world diverged from the original on the next tick.
	w.rebuildClosures()

	return nil
}

// GrantClasses licenses car classes without running the growth schedule. It
// exists for tests and tooling that need a progressed world directly; gameplay
// reaches these states through scheduled growth.
func (w *World) GrantClasses(classes []string) {
	for _, class := range classes {
		w.buyableClasses[class] = true
	}
}

// Clone returns a deep copy of the ledger, including its per-item counters,
// cash statement and the monthly summaries derived from it.
func (l *Ledger) Clone() Ledger {
	out := *l
	out.DeliveriesByItem = make(map[string]int, len(l.DeliveriesByItem))
	for k, v := range l.DeliveriesByItem {
		out.DeliveriesByItem[k] = v
	}
	out.Cash = append([]CashEntry(nil), l.Cash...)
	out.ReputationEntries = append([]ReputationEntry(nil), l.ReputationEntries...)
	out.Months = make([]MonthSummary, len(l.Months))
	for i := range l.Months {
		out.Months[i] = l.Months[i]
		out.Months[i].Categories = append([]MonthCategory(nil), l.Months[i].Categories...)
	}
	return out
}

func orEmptyInt64(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

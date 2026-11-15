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
// City mutations are NOT stored a second time. Built names the completed builds
// and ImportState replays their effects, so there is exactly one description of
// what a build does: the city config.

// SavedState is the restartable half of a World: everything that is not derived
// from the city file.
type SavedState struct {
	Tick   int64   `json:"tick"`
	Seed   uint64  `json:"seed"`
	Ledger *Ledger `json:"ledger"`

	Cars   []*Car   `json:"cars"`
	Orders []*Order `json:"orders"`

	NextCarSeq int `json:"next_car_seq"`
	OrderSeq   int `json:"order_seq"`

	// Built is the authoritative record of what the city has become. Permitted
	// and BuyableClasses are the economy half of progression; Achievements
	// records which one-shot triggers already fired, so reloading cannot pay
	// their rewards a second time.
	Built          map[string]bool `json:"built"`
	Permitted      map[string]bool `json:"permitted"`
	BuyableClasses map[string]bool `json:"buyable_classes"`
	Achievements   map[string]bool `json:"achievements"`

	// Order spawner memory. Without it a reload cannot tell a fresh city from a
	// resumed one, so every reload raised the forced first order again.
	NextEligibleTick map[string]int64 `json:"next_eligible_tick"`
	LastSpawnTick    int64            `json:"last_spawn_tick"`
	HasSpawned       bool             `json:"has_spawned"`

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
		Built:            cloneBoolMap(w.built),
		Permitted:        cloneBoolMap(w.permitted),
		BuyableClasses:   cloneBoolMap(w.buyableClasses),
		Achievements:     map[string]bool{},
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
	for i := range w.achievements {
		if w.achievements[i].Done {
			s.Achievements[w.achievements[i].ID] = true
		}
	}
	for k, v := range w.spawn.NextEligibleTick {
		s.NextEligibleTick[k] = v
	}
	return s
}

// ImportState restores a world from a saved state. The world must have been
// built from the same city file as the save.
func (w *World) ImportState(s SavedState) error {
	// Geography first, in config order so the result is deterministic.
	for _, b := range w.builds {
		if !s.Built[b.ID] {
			continue
		}
		if err := w.City.applyEffects(b.Effects); err != nil {
			return err
		}
	}

	if s.Ledger != nil {
		w.Ledger = s.Ledger
	}
	if w.Ledger.DeliveriesByItem == nil {
		w.Ledger.DeliveriesByItem = map[string]int{}
	}
	if s.Cars != nil {
		w.Cars = s.Cars
	}
	if s.Orders != nil {
		w.Orders = s.Orders
	}

	w.Tick = s.Tick
	w.NextCarSeq = s.NextCarSeq
	w.orderSeq = s.OrderSeq
	w.built = orEmpty(s.Built)
	w.permitted = orEmpty(s.Permitted)
	w.buyableClasses = orEmpty(s.BuyableClasses)

	w.spawn.NextEligibleTick = orEmptyInt64(s.NextEligibleTick)
	w.spawn.LastSpawnTick = s.LastSpawnTick
	w.spawn.HasSpawned = s.HasSpawned

	// Rebuild each car's box load from its cargo and the orders' sizes.
	// cargoBoxes is unexported and therefore not serialised; recomputing it
	// here is what keeps it derived state rather than a second source of truth
	// that a reload could silently disagree with.
	for _, c := range w.Cars {
		c.cargoBoxes = 0
		for _, id := range c.Cargo {
			if o, ok := w.Order(id); ok {
				c.cargoBoxes += o.Boxes()
			}
		}
	}

	for i := range w.achievements {
		w.achievements[i].Done = s.Achievements[w.achievements[i].ID]
	}
	for i := range w.unlocks {
		w.unlocks[i].Earned = w.permitted[buildOfUnlock(&w.unlocks[i])]
	}

	if s.Events != nil {
		w.Events = s.Events
	}

	// Street works are derived from (seed, tick, chapter) rather than stored, so
	// the in-memory cache has to be rebuilt for the restored tick. Skipping this
	// made a save taken during road works reload with the street open, and the
	// reloaded world diverged from the original on the next tick.
	w.rebuildClosures()

	return nil
}

// Grant permits builds and car classes without charging for them. It exists for
// tests and tooling that need a progressed world directly; gameplay reaches
// these states through progression.
func (w *World) Grant(permittedBuilds, buyableClasses []string) {
	for _, id := range permittedBuilds {
		w.permitted[id] = true
	}
	for _, class := range buyableClasses {
		w.buyableClasses[class] = true
	}
}

// Clone returns a deep copy of the ledger, including its per-item counters.
func (l *Ledger) Clone() Ledger {
	out := *l
	out.DeliveriesByItem = make(map[string]int, len(l.DeliveriesByItem))
	for k, v := range l.DeliveriesByItem {
		out.DeliveriesByItem[k] = v
	}
	return out
}

func cloneBoolMap(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func orEmpty(m map[string]bool) map[string]bool {
	if m == nil {
		return map[string]bool{}
	}
	return m
}

func orEmptyInt64(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

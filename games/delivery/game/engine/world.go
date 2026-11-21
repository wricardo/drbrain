package engine

import (
	"fmt"
	"sort"
)

// World is one playthrough: the city, the cars on it, and the clock.
//
// It owns the tick loop. Every state change the player can observe happens in
// exactly one of two places: a command (queuing a move, buying a car) or a
// tick (resolving queued moves, charging). Nothing else mutates the world,
// which keeps simulation reproducible.
//
// Reproducibility rests on a derived, not stored, model: closures and order
// jitter are pure functions of (seed, tick, chapter). w.closures is only a
// cache of what has been announced — ExportState does not save it, and
// ImportState calls rebuildClosures to re-derive the window around the restored
// tick. A save taken during street works therefore reloads with the same
// closure on the same tiles and stays in step, and a replay from the seed
// reaches the same streets on the same ticks. Verified at tick 802:
// cl_801 on [(15,2) (15,3) (15,4)] on both sides, identical one tick later.
//
// Because the derivation consults reachability (closureIsSafe), fog is an input
// to it: two worlds with different revealed areas can derive different closures
// at the same tick, which matters when comparing a probe's results.
type World struct {
	// City is the session's terrain and static entities. Scheduled growth
	// mutates it (revealing districts, rewriting tiles); the source CityConfig
	// is never changed.
	City *City
	// Cars is the fleet in creation order. Commands address cars by ID, and
	// order-sensitive tick phases sort by ID, so append order never changes a
	// rule.
	Cars []*Car

	// carClasses is the catalogue this world was built from, so buying a car
	// does not need the original config.
	carClasses map[string]CarClassConfig

	// Tick is the number of ticks advanced so far. It starts at 0.
	Tick int64

	// Paused stops Advance from doing anything. Commands are still accepted
	// while paused so a player can set up a move before resuming.
	Paused bool

	// Speed is the simulation multiplier (1, 2 or 4). It does not change what
	// a tick does, only how often the driver calls Advance.
	Speed int

	// NextCarSeq is the counter behind generated car ids.
	NextCarSeq int

	// Orders holds every order ever raised this session, in creation order.
	Orders []*Order

	// orderSeq is the counter behind generated order ids.
	orderSeq int

	// Ledger is money, reputation and the achievement counters.
	Ledger *Ledger

	// Seed makes every random decision reproducible. Spawning, jitter and
	// reward rounding all derive from (Seed, Tick, a label) rather than from a
	// stateful stream, so replay is exact.
	Seed uint64

	// spawn is the order-spawner's per-customer memory.
	spawn spawnState

	// ordersCfg is the pacing/economy table this world was built with.
	ordersCfg OrdersConfig

	// closuresCfg is the street-works pacing table.
	closuresCfg ClosureConfig

	// closures holds the closures that have started and not yet expired.
	// They are re-derived from (seed, tick, chapter) rather than read from a
	// save, so this slice is a cache of what has already been announced, not
	// authoritative game state.
	closures []Closure

	// fleet is the vehicle running-cost table, copied from the config.
	fleet FleetConfig

	// ticksPerMonth is the accounting month length for the monthly report.
	ticksPerMonth int64

	// maxEvents bounds the retained event feed.
	maxEvents int

	// maxReputation is the top of the reputation scale.
	maxReputation float64

	// maxChapter caps the chapter index used by tuning tables.
	maxChapter int

	// autosaveTicks is how many ticks may pass between background saves.
	autosaveTicks int64

	// growth is the city's expansion schedule, copied from the config at
	// construction so a running world never reads back from the CityConfig.
	growth []GrowthConfig

	// growthApplied is how many stages at the head of growth have applied. It is
	// derived, not saved: ImportState replays every stage whose tick has been
	// reached, so a reload lands on the same city without a second source of
	// truth in the save file.
	growthApplied int

	// buyableClasses is the car classes licensed by growth stages so far. Also
	// derived from the replayed schedule; it gates requires_grant classes only.
	buyableClasses map[string]bool

	// Events accumulates what happened, newest last, for the player's feed.
	Events []Event

	// currentTickEvents collects the events of the tick in progress, so Advance
	// can return exactly what this tick produced. Slicing the retained event log
	// instead was wrong once the log trimmed: the tail of the log and the tail
	// of the tick are different things.
	currentTickEvents []Event
}

// Event is one thing that happened in the world, for the client feed.
type Event struct {
	// Tick is the world tick that produced the event.
	Tick int64
	// Type is one of the Event* constants; clients key filters and icons off it.
	Type string
	// Message is display text for the feed.
	Message string
	// CarID and Pos name the subject when the event has one.
	CarID string
	Pos   Position
}

// Event type names.
const (
	EventMoved      = "car_moved"
	EventBlocked    = "car_blocked"
	EventCharged    = "car_charged"
	EventStranded   = "car_stranded"
	EventBoughtCar  = "car_bought"
	EventPickedUp   = "order_picked_up"
	EventDelivered  = "order_delivered"
	EventExpired    = "order_expired"
	EventSpawned    = "order_spawned"
	EventGrowth     = "city_growth"
	EventClosure    = "closure_started"
	EventClosureEnd = "closure_ended"
)

// NewWorld builds a world with the fixed default seed, for tests and tooling
// where runs must be comparable. It validates the config and installs the
// InitialCars exactly as NewWorldSeeded does; real sessions use NewWorldSeeded
// with RandomSeed instead.
func NewWorld(city *City, cfg *CityConfig) (*World, error) {
	return NewWorldSeeded(city, cfg, defaultSeed)
}

// defaultSeed is the fixed seed used when a caller does not care. Sessions in
// the real game pass a random one; tests and the harness rely on this being
// stable so runs are comparable.
const defaultSeed uint64 = 0x5eed5eed

// NewWorldSeeded builds a world with an explicit seed. Order spawning, jitter,
// rewards and closures all derive from (seed, tick, label), so the same seed and
// commands replay exactly. It validates cfg even when it was built in code,
// places InitialCars in authored order, and returns an error for an unwalkable
// or over-capacity start tile.
func NewWorldSeeded(city *City, cfg *CityConfig, seed uint64) (*World, error) {
	// Validate here as well as in the loader, so a config built in code
	// (tests, the harness, an API caller) fails loudly on a missing knob
	// instead of silently running with a zeroed multiplier.
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.validateRewardCoverage(); err != nil {
		return nil, err
	}
	w := &World{
		City:           city,
		Speed:          1,
		carClasses:     cfg.CarClasses,
		Ledger:         NewLedger(cfg.StartMoney),
		Seed:           seed,
		ordersCfg:      cfg.Orders,
		closuresCfg:    *cfg.Closures,
		fleet:          *cfg.Fleet,
		ticksPerMonth:  int64(cfg.TicksPerMonth),
		maxEvents:      cfg.MaxEvents,
		maxReputation:  cfg.MaxReputation,
		maxChapter:     cfg.MaxChapter,
		autosaveTicks:  int64(cfg.AutosaveTicks),
		growth:         append([]GrowthConfig(nil), cfg.Growth...),
		buyableClasses: map[string]bool{},
	}
	w.spawn.NextEligibleTick = map[string]int64{}

	// Deterministic car ordering: the sim and every replay depend on it.
	for i, ic := range cfg.InitialCars {
		pos := ic.Pos
		if !city.Walkable(pos) {
			return nil, fmt.Errorf("initial car %d at %s is not on walkable terrain", i, pos)
		}
		// Authored positions must respect the same occupancy limit the mover
		// enforces, or a map could start in a state the engine would never
		// allow a car to drive into: two cars sharing a road tile, both stuck
		// reporting `occupied` the moment either tries to leave and come back.
		if w.tileFull(pos) {
			return nil, fmt.Errorf("initial car %d at %s overlaps %d other car(s); %s holds at most %d",
				i, pos, w.carsAt(pos), pos, city.CarCapacity(pos))
		}
		name := ic.Name
		if name == "" {
			name = fmt.Sprintf("Car %d", i+1)
		}
		car, err := NewCar(fmt.Sprintf("car_%d", i+1), name, ic.Class, pos, cfg.CarClasses)
		if err != nil {
			return nil, err
		}
		w.Cars = append(w.Cars, car)
	}
	w.NextCarSeq = len(cfg.InitialCars) + 1
	if err := w.validateGrowthSchedule(); err != nil {
		return nil, err
	}
	return w, nil
}

// TicksPerMonth returns the accounting month length from the map config.
func (w *World) TicksPerMonth() int { return int(w.ticksPerMonth) }

// AutosaveTicks returns how many world ticks may pass between background saves.
func (w *World) AutosaveTicks() int64 { return w.autosaveTicks }

// MonthForTick returns the 1-based accounting month containing tick using this
// world's configured month length. Month 1 is ticks 0..TicksPerMonth()-1;
// negative ticks report month 1. There is no wall calendar: months exist only
// to bucket the cash statement.
func (w *World) MonthForTick(tick int64) int {
	return monthForTick(tick, w.ticksPerMonth)
}

// Car returns the live car with the given id, not a copy. Mutating it changes
// the world; on a running session, do it inside Session.Do so the ticker sees a
// consistent state.
func (w *World) Car(id string) (*Car, bool) {
	for _, c := range w.Cars {
		if c.ID == id {
			return c, true
		}
	}
	return nil, false
}

// QueueMove sets a car's queued direction. This is the manual driving command:
// it does not move the car, it schedules exactly one step for the next tick.
//
// Queueing is deliberately not an error when the move would be illegal; the
// rejection happens at tick time so the player sees the world's answer (and
// so a wall does not silently swallow a command before the tick it applies
// to). The one early exit is an unknown car or direction, which are caller
// bugs rather than gameplay.
func (w *World) QueueMove(carID string, dir Direction) error {
	car, ok := w.Car(carID)
	if !ok {
		return fmt.Errorf("no car %q", carID)
	}
	if !dir.Valid() {
		return fmt.Errorf("bad direction %q", dir)
	}
	car.QueuedMove = dir
	return nil
}

// Advance runs exactly one tick and returns the events it produced. A paused
// world does nothing and returns nil.
//
// The order is fixed so a replay of (seed, commands) reproduces the world:
//  1. plan every car's queued move, then resolve the fleet together in car id
//     order (a follower may enter a vacated tile; a swap is refused)
//  2. run pickups/dropoffs for cars that actually moved
//  3. charge cars on charging tiles, assigning depot charge slots in car id order
//  4. apply city growth due this tick, before spawning, so a district revealed
//     now can raise work on the same tick
//  5. spawn due orders
//  6. expire overdue orders
//  7. retire and start street works, after movement, so a closure announced
//     now cannot retroactively refuse a move already committed
//  8. bill fleet upkeep, after movement and charging, so a car that parks this
//     tick is free for it, then increment Tick
func (w *World) Advance() []Event {
	if w.Paused {
		return nil
	}
	// Events are captured as they are emitted rather than sliced off the log at
	// the end: the log is trimmed to max_events, and a slice of the trimmed log
	// silently returned nothing once the trim point passed the tick's first
	// event.
	w.currentTickEvents = w.currentTickEvents[:0]

	// 1. Moves. Sorting by id (not slice order) makes the result independent
	// of how cars were appended, so buying a car never changes how existing
	// cars behave on the same tick.
	ordered := make([]*Car, len(w.Cars))
	copy(ordered, w.Cars)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	// Plan first, resolve second. Planning consumes each queued move and applies
	// the checks that do not depend on the rest of the fleet; resolving then
	// decides between blocked moves together, so a follower is admitted and a
	// head-on swap is not. Resolving per car, one at a time, made the answer
	// depend on id order rather than on whether there was room.
	plans := make([]plannedMove, 0, len(ordered))
	for _, car := range ordered {
		if plan, ok := w.planMove(car); ok {
			plans = append(plans, plan)
		}
	}
	moved := w.resolveMoves(plans)

	// 2. Pickups and dropoffs, only for cars that actually moved. A car parked
	// on a kitchen with a freshly assigned order collects on its next step,
	// not by standing still — otherwise assignment alone would complete work.
	for _, car := range moved {
		w.resolveTileInteractions(car)
	}

	// 3. Charging. A car on a charging tile regains charge over ticks rather
	// than instantly, which is what forces planning instead of teleporting
	// home for free. A depot with fewer charge slots than parking spaces serves
	// only the first slots drivers in car id order; the rest stay put and gain
	// nothing this tick, so a crowded yard drains in id order instead of all at
	// once. Cars already full take no slot, so they never block a charge.
	charged := make(map[Position]int)
	for _, car := range ordered {
		w.chargeCar(car, charged)
	}

	// 3b. City growth: stages due at or before this tick apply before orders are
	// spawned, so a district revealed now can raise work on the same tick.
	w.applyGrowth()

	// 4-5. Order lifecycle.
	w.spawnOrders()
	w.expireOrders()

	// 5b -> 6. Street works: retire last tick's closures and start any due now.
	// This runs AFTER movement, so a closure announced on this tick never
	// retroactively blocks a move the player already committed to.
	w.stepClosures()

	// 7. Fleet upkeep. Billed after movement, charging and closures so a car
	// that parks during this tick is free for it: the player's action lands
	// first and the bill follows.
	w.chargeUpkeep()

	w.Tick++
	return append([]Event(nil), w.currentTickEvents...)
}

// advanceCar runs the movement half of a tick for one car. It is used by tests
// that want to check a single movement rule without advancing the clock, and
// deliberately shares stepCar's validation rather than duplicating it.
func (w *World) advanceCar(car *Car) bool {
	return w.stepCar(car)
}

// AdvanceNoPause runs one tick regardless of the Paused flag.
//
// Paused is a session-level convenience (sessions start paused because driving
// is manual), not a game rule, so simulation tests and the harness need a way
// to step the world without first unpausing. It shares the body of Advance.
func (w *World) AdvanceNoPause() []Event {
	wasPaused := w.Paused
	w.Paused = false
	evs := w.Advance()
	w.Paused = wasPaused
	return evs
}

// stepCar attempts the car's queued move for this tick. It reports whether the
// car actually changed position, which is what decides if tile interactions
// run.
//
// It is a one-car tick: the car is planned, then resolved as the only mover.
// The occupancy fixpoint in resolveMoves therefore sees every other car as
// stationary, which is exactly the right answer for a single step. Advance goes
// through the same two phases with the whole fleet, so the rules cannot drift
// between the tested single-car path and the real one.
func (w *World) stepCar(car *Car) bool {
	plan, ok := w.planMove(car)
	if !ok {
		return false
	}
	return len(w.resolveMoves([]plannedMove{plan})) > 0
}

// plannedMove is a move that has passed every check that does not depend on the
// other cars this tick: direction, street works, terrain and battery.
type plannedMove struct {
	car  *Car
	dir  Direction
	next Position
	cost int
}

// planMove validates the car's queued move and consumes the queue. The
// rejections it can emit are all static — none of them depend on what the other
// cars did this tick — which is what lets Advance decide the whole fleet's moves
// before applying any of them.
func (w *World) planMove(car *Car) (plannedMove, bool) {
	dir := car.QueuedMove
	car.QueuedMove = ""
	if dir == "" {
		return plannedMove{}, false
	}

	if car.Battery <= 0 {
		car.LastReject = RejectNoBattery
		w.emit(EventBlocked, car, fmt.Sprintf("%s has no battery left", car.Name))
		return plannedMove{}, false
	}

	next, ok := car.Pos.Neighbour(dir)
	if !ok {
		car.LastReject = RejectBadDirection
		return plannedMove{}, false
	}
	// Street works close a tile to ENTRY. Leaving one is always allowed: the
	// check is on the destination, so a car standing on a tile that closes
	// under it can still drive off. Sealing a car in with a scheduled event
	// the player did not choose would be a bug with a timer, not a decision.
	if w.ClosedAt(next) {
		car.LastReject = RejectClosed
		w.emit(EventBlocked, car,
			fmt.Sprintf("%s cannot go %s: street closed for works", car.Name, dir))
		return plannedMove{}, false
	}
	if !w.City.Walkable(next) {
		t, onGrid := w.City.At(next)
		if !onGrid {
			// The grid edge is a different answer from fog or terrain, and
			// reporting it as "unexplored" made a map boundary look like a
			// district the player had not bought yet.
			car.LastReject = RejectOffGrid
			w.emit(EventBlocked, car,
				fmt.Sprintf("%s cannot go %s: that is the edge of the map", car.Name, dir))
			return plannedMove{}, false
		}
		car.LastReject = RejectBlocked
		w.emit(EventBlocked, car,
			fmt.Sprintf("%s cannot go %s: %s", car.Name, dir, w.blockReason(next, t)))
		return plannedMove{}, false
	}

	cost := w.City.WalkCost(next, car.Class)
	if car.Battery < cost {
		// Not enough charge to enter this tile. The car stays put; it is not
		// stranded yet unless it has no charge at all.
		car.LastReject = RejectNoBattery
		w.emit(EventBlocked, car,
			fmt.Sprintf("%s needs %d battery for %s, has %d", car.Name, cost, dir, car.Battery))
		return plannedMove{}, false
	}

	return plannedMove{car: car, dir: dir, next: next, cost: cost}, true
}

// resolveMoves decides which of this tick's planned moves actually happen, then
// applies them. plans must be in car-id order; the result is deterministic
// regardless of how the fleet slice was built.
//
// The decision is a fixpoint rather than a single pass, and that is the whole
// point:
//
//   - A single pass that reads live positions lets a car follow another into the
//     tile it just left only if the leader happens to be resolved first. With
//     one shared rule the order would decide the outcome, so the rule would be
//     "lower id wins", not "there is room".
//   - A single pass that ignores other moving cars cannot tell a follower from a
//     head-on swap, and would let two cars pass through each other.
//
// So each round retries the moves that were blocked, against the positions every
// car would hold if the moves decided so far all went through. A follower's
// destination empties as its leader commits and it goes on a later round; a
// head-on pair each occupies the other's destination in every round, so neither
// ever commits. Rounds stop when a full pass commits nothing, which is at most
// len(plans) rounds.
//
// Committing is a separate final pass in id order so the event log stays in car
// id order — the fixpoint may commit a follower before its leader, and a feed
// that reported the follower first would read as if it had overtaken.
func (w *World) resolveMoves(plans []plannedMove) []*Car {
	if len(plans) == 0 {
		return nil
	}

	// Where each car would stand given the moves decided so far. Stationary cars
	// are in here at their current position, which is what makes a car's tile
	// count as occupied until that car actually moves.
	pos := make(map[string]Position, len(w.Cars))
	for _, c := range w.Cars {
		pos[c.ID] = c.Pos
	}
	countAt := func(p Position) int {
		n := 0
		for _, q := range pos {
			if q == p {
				n++
			}
		}
		return n
	}

	decided := make(map[string]bool, len(plans))
	pending := plans
	for len(pending) > 0 {
		var still []plannedMove
		for _, mv := range pending {
			if countAt(mv.next) < w.City.CarCapacity(mv.next) {
				pos[mv.car.ID] = mv.next
				decided[mv.car.ID] = true
				continue
			}
			still = append(still, mv)
		}
		if len(still) == len(pending) {
			// No progress: every remaining move is blocked by a car that is
			// itself staying put. Retrying is pointless.
			break
		}
		pending = still
	}

	moved := make([]*Car, 0, len(plans))
	for _, mv := range plans {
		car := mv.car
		if !decided[car.ID] {
			car.LastReject = RejectOccupied
			w.emit(EventBlocked, car,
				fmt.Sprintf("%s cannot go %s: another car is already there", car.Name, mv.dir))
			continue
		}

		car.Pos = mv.next
		car.Battery -= mv.cost
		car.StepsTaken++
		car.LastReject = RejectNone
		w.emit(EventMoved, car, fmt.Sprintf("%s moved %s", car.Name, mv.dir))

		if car.Battery <= 0 && !w.City.ChargingAt(car.Pos) {
			w.emit(EventStranded, car, fmt.Sprintf("%s is out of battery at %s", car.Name, car.Pos))
		}
		moved = append(moved, car)
	}
	return moved
}

// CarsAt counts the cars currently standing on p.
//
// Exported because contention is a player-facing fact now that a tile has a car
// capacity: a tool, a UI badge or the simulation harness all need to ask "is
// this charger taken" without re-deriving it from the fleet and getting a
// different answer from the mover.
func (w *World) CarsAt(p Position) int {
	n := 0
	for _, c := range w.Cars {
		if c.Pos == p {
			n++
		}
	}
	return n
}

// carsAt is the internal spelling used by the mover.
func (w *World) carsAt(p Position) int { return w.CarsAt(p) }

// HasRoom reports whether p holds one more car right now: it compares the live
// car count with City.CarCapacity. Depots are unlimited, a standalone charger
// holds its authored capacity (default 1), every other on-grid tile holds one,
// and off-grid holds none. This checks occupancy only, not whether a car could
// drive there.
func (w *World) HasRoom(p Position) bool {
	return w.carsAt(p) < w.City.CarCapacity(p)
}

// tileFull reports whether p already holds as many cars as the terrain allows.
//
// It is a live scan of car positions rather than a cached occupancy grid on
// purpose: positions are the only truth, every mutation site already updates
// car.Pos, and a cache is one more thing a reload or a Tow could get wrong. The
// fleet is a handful of cars, so the scan costs nothing.
//
// It answers "is this tile full right now", which is the question BuyCar and Tow
// ask. Movement does NOT call it: a mover needs to know what the tile will hold
// after this tick's other moves, which is what resolveMoves computes.
func (w *World) tileFull(p Position) bool {
	limit := w.City.CarCapacity(p)
	if limit <= 0 {
		return true
	}
	return w.carsAt(p) >= limit
}

// blockReason explains why a walkable-looking tile is not enterable.
//
// Hidden is checked before terrain because a hidden tile's terrain is a
// secret: reporting "water" for a fogged tile would leak the map.
func (w *World) blockReason(p Position, t CellType) string {
	if w.City.IsHidden(p) {
		return "unexplored"
	}
	if !t.Passable() {
		return string(t)
	}
	return "unreachable"
}

// chargeCar tops a car up by the tile's charge rate when it sits on a charging
// tile, subject to the tile's simultaneous-charging limit.
//
// Depot charging is free; a standalone charger accrues a bill on the car and
// writes it to the checking account once, when the car fills or leaves the
// bay. A ledger row per tick would bury deliveries under a dozen identical
// lines for one top-up.
//
// charged counts, per position, how many cars have already taken a charge this
// tick. The caller iterates cars in id order, which is what makes the answer
// deterministic when a depot has more cars than slots. A car that is already
// full returns before touching that count, so parking a charged car does not
// consume a bay.
func (w *World) chargeCar(car *Car, charged map[Position]int) {
	if !w.City.ChargingAt(car.Pos) || car.Full() {
		w.settleCharging(car)
		return
	}
	rate := w.chargeRate(car.Pos)
	price := w.chargePrice(car.Pos)
	source := w.chargeSource(car.Pos)
	// Moving from one paid charger to another (or onto a free source) ends the
	// previous session before a new one starts, so the memo names the right
	// charger and two sessions cannot merge into one line.
	if car.ChargeDue > 0 && (price <= 0 || car.ChargeSource != source) {
		w.settleCharging(car)
	}
	if slots := w.City.ChargeSlotsAt(car.Pos); slots > 0 && charged[car.Pos] >= slots {
		return
	}
	before := car.Battery
	car.Battery = min(car.Battery+rate, car.Class.MaxBattery)
	gained := car.Battery - before
	if gained <= 0 {
		return
	}
	charged[car.Pos]++
	cost := 0
	if price > 0 {
		cost = chargeCost(price, rate, gained)
		car.ChargeDue += cost
		car.ChargeUnits += gained
		car.ChargeSource = source
	}
	msg := fmt.Sprintf("%s charging %d/%d", car.Name, car.Battery, car.Class.MaxBattery)
	if cost > 0 {
		msg += fmt.Sprintf(" (-$%d)", cost)
	}
	w.emit(EventCharged, car, msg)
	if car.Full() {
		w.settleCharging(car)
	}
}

// chargeCost prices one tick of charging from the energy it actually
// delivered. PricePerTick is the price of a full tick at rate units, so a
// partial tick costs proportionally less; rounding up keeps a one-unit top-up
// from being free. With price $1 and rate 2, one unit costs $1 and a full tick
// also costs $1; with price $4 and rate 6, one unit costs $1 and a full tick
// costs $4. This is the fair-by-energy model without fractional dollars.
func chargeCost(price, rate, gained int) int {
	if price <= 0 || rate <= 0 || gained <= 0 {
		return 0
	}
	cost := (price*gained + rate - 1) / rate
	if cost < 1 {
		cost = 1
	}
	return cost
}

// settleCharging writes the car's open charging session to the cash ledger and
// clears it. It is a no-op when the car owes nothing, so callers can call it on
// every tile change.
func (w *World) settleCharging(car *Car) {
	due := car.ChargeDue
	units := car.ChargeUnits
	source := car.ChargeSource
	car.ChargeDue = 0
	car.ChargeUnits = 0
	car.ChargeSource = ""
	if due <= 0 {
		return
	}
	if source == "" {
		source = "charger"
	}
	unitWord := "units"
	if units == 1 {
		unitWord = "unit"
	}
	w.recordCash(CashCharging, -due, car, "", fmt.Sprintf("%d %s at %s", units, unitWord, source))
}

// chargeRate is how much battery a tile restores per tick. Depots use the
// city's depot rate; a standalone charger uses its own rate, which the city
// normalises to charging.charger_rate at load. Any other tile returns zero:
// unlike the old implementation, this no longer reports a charger rate for
// plain road.
func (w *World) chargeRate(p Position) int {
	t, ok := w.City.At(p)
	if !ok {
		return 0
	}
	switch t {
	case DepotTile:
		return w.City.Charging.DepotRate
	case ChargerTile:
		if ch, ok := w.City.ChargerAt(p); ok {
			return ch.Rate
		}
		return w.City.Charging.ChargerRate
	}
	return 0
}

// chargePrice is the dollars charged for one full tick on p. Depots and
// non-charging tiles are free; a standalone charger uses its own price or the
// city default.
func (w *World) chargePrice(p Position) int {
	t, ok := w.City.At(p)
	if !ok || t != ChargerTile {
		return 0
	}
	if ch, ok := w.City.ChargerAt(p); ok {
		return ch.PricePerTick
	}
	return w.City.Charging.ChargerPricePerTick
}

// chargeSource names the paid charger in the ledger memo.
func (w *World) chargeSource(p Position) string {
	if ch, ok := w.City.ChargerAt(p); ok && ch.ID != "" {
		return ch.ID
	}
	return "charger"
}

// BuyCar purchases a car of the named class at the named depot, spending its
// price. Multi-car is gated by money alone: the engine has always supported N
// cars, so buying is the only thing standing between a new player and a fleet.
func (w *World) BuyCar(class, depotID string) (*Car, error) {
	cc, ok := w.carClasses[class]
	if !ok {
		return nil, fmt.Errorf("unknown car class %q", class)
	}
	if err := w.BuyCarClass(class); err != nil {
		return nil, err
	}
	depot, ok := w.depot(depotID)
	if !ok {
		return nil, fmt.Errorf("no depot %q", depotID)
	}
	if w.Ledger.Money < cc.Price {
		return nil, fmt.Errorf("%s costs $%d, you have $%d", class, cc.Price, w.Ledger.Money)
	}

	id := fmt.Sprintf("car_%d", w.NextCarSeq)
	name := fmt.Sprintf("%s %d", titleWord(class), w.NextCarSeq)
	car, err := NewCar(id, name, class, depot.Pos, map[string]CarClassConfig{class: cc})
	if err != nil {
		return nil, err
	}
	w.recordCash(CashCarPurchase, -cc.Price, car, "", "bought "+car.Name)
	w.Cars = append(w.Cars, car)
	w.NextCarSeq++
	w.emit(EventBoughtCar, car, fmt.Sprintf("bought %s at %s for $%d", car.Name, depotID, cc.Price))
	return car, nil
}

// depot looks up a depot by id.
func (w *World) depot(id string) (Depot, bool) {
	for _, d := range w.City.Depots {
		if d.ID == id {
			return d, true
		}
	}
	return Depot{}, false
}

// Tow returns a car to its nearest depot for a distance-priced fee.
//
// The tow stays available to a healthy car, not just a stranded one: a recall is
// a legitimate convenience, and refusing it would make a mis-parked car a
// problem with no solution. What the fee removes is the version that shipped
// before this, where an unconditional free teleport made every running-cost rule
// in the game optional.
//
// The fee is CLAMPED to the balance rather than refusing the tow, and
// deliberately so: a player who is broke AND stranded must still be able to get
// their car back, or the only escape from an empty battery at $0 is abandoning
// the save. A tow therefore can never deadlock, and it can never make money
// negative.
func (w *World) Tow(carID string) error {
	car, ok := w.Car(carID)
	if !ok {
		return fmt.Errorf("no car %q", carID)
	}
	if len(w.City.Depots) == 0 {
		return fmt.Errorf("no depot to tow to")
	}
	// Nearest depot by walking distance, falling back to the first depot if
	// the car cannot reach any (which is exactly the stranded case). Depots
	// have unlimited parking, so there is no yard-capacity choice any more.
	dest := w.City.Depots[0]
	best := -1
	fallback := w.City.Depots[0]
	fallbackBest := -1
	for _, d := range w.City.Depots {
		dist := w.towDistance(car.Pos, d.Pos)
		if dist >= 0 && (fallbackBest < 0 || dist < fallbackBest) {
			fallbackBest = dist
			fallback = d
		}
		if w.tileFull(d.Pos) {
			continue
		}
		if dist >= 0 && (best < 0 || dist < best) {
			best = dist
			dest = d
		}
	}
	if best < 0 {
		dest = fallback
	}

	// A car towed off a paid charger still owes for the energy it already
	// received; settle that session before the tow fee so the statement shows
	// both movements in the order they happened.
	w.settleCharging(car)

	fee := w.TowFee(car, dest.Pos)
	fee = -w.recordCash(CashTow, -fee, car, "", "tow to "+dest.ID)
	w.Ledger.TowFees += fee

	car.Pos = dest.Pos
	car.Battery = 1 // towed in, not repaired: it must charge up properly
	car.QueuedMove = ""
	car.LastReject = RejectNone
	w.Ledger.Tows++
	if fee > 0 {
		w.emit(EventCharged, car, fmt.Sprintf("%s towed to %s (-$%d)", car.Name, dest.ID, fee))
	} else {
		w.emit(EventCharged, car, fmt.Sprintf("%s towed to %s", car.Name, dest.ID))
	}
	return nil
}

// emit appends an event at the current tick.
//
// Events are kept for client feeds, but a session can run for hundreds of
// thousands of ticks, so the log is trimmed to the most recent max_events. Only
// the tail is ever useful: an agent polls for what changed since its last read.
func (w *World) emit(kind string, car *Car, msg string) {
	ev := Event{Tick: w.Tick, Type: kind, Message: msg}
	if car != nil {
		ev.CarID = car.ID
		ev.Pos = car.Pos
	}
	w.Events = append(w.Events, ev)
	w.currentTickEvents = append(w.currentTickEvents, ev)
	if len(w.Events) > w.maxEvents {
		// Copy down rather than reslice, so the backing array does not grow
		// without bound across a long session.
		keep := len(w.Events) - w.maxEvents
		copy(w.Events, w.Events[keep:])
		w.Events = w.Events[:w.maxEvents]
	}
}

// RenderCars adapts the fleet for the ASCII renderer.
func (w *World) RenderCars() []RenderCar {
	out := make([]RenderCar, len(w.Cars))
	for i, c := range w.Cars {
		out[i] = RenderCar{Pos: c.Pos}
	}
	return out
}

// titleWord upper-cases the first letter of a class name for display.
func titleWord(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

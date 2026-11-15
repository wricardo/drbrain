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
type World struct {
	City *City
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

	// charging is the battery-refill rate table.
	charging ChargingConfig

	// Progression tables, copied from the config at construction so a running
	// world never reads back from the CityConfig.
	achievements []AchievementConfig
	unlocks      []UnlockConfig
	builds       []BuildConfig

	// achievements/unlocks mutate as they fire, so they keep their own status.
	built          map[string]bool
	permitted      map[string]bool // build id -> unlocked
	buyableClasses map[string]bool // car class -> purchase allowed

	// Events accumulates what happened, newest last, for the player's feed.
	Events []Event

	// currentTickEvents collects the events of the tick in progress, so Advance
	// can return exactly what this tick produced. Slicing the retained event log
	// instead was wrong once the log trimmed: the tail of the log and the tail
	// of the tick are different things.
	currentTickEvents []Event
}

// buildOfUnlock reports the first build an unlock grants, or "" when it grants
// none.
func buildOfUnlock(u *UnlockConfig) string {
	if len(u.GrantsBuild) == 0 {
		return ""
	}
	return u.GrantsBuild[0]
}

// Event is one thing that happened in the world, for the client feed.
type Event struct {
	Tick    int64
	Type    string
	Message string
	CarID   string
	Pos     Position
}

// Event type names.
const (
	EventMoved     = "car_moved"
	EventBlocked   = "car_blocked"
	EventCharged   = "car_charged"
	EventStranded  = "car_stranded"
	EventBoughtCar = "car_bought"
	EventPickedUp  = "order_picked_up"
	EventDelivered = "order_delivered"
	EventExpired   = "order_expired"
	EventSpawned   = "order_spawned"
	EventBuild     = "build_completed"
	EventUnlock    = "unlock_earned"
)

// NewWorld builds a world from a city and the config that produced it,
// placing the initial cars at their authored positions.
func NewWorld(city *City, cfg *CityConfig) (*World, error) {
	return NewWorldSeeded(city, cfg, defaultSeed)
}

// defaultSeed is the fixed seed used when a caller does not care. Sessions in
// the real game pass a random one; tests and the harness rely on this being
// stable so runs are comparable.
const defaultSeed uint64 = 0x5eed5eed

// NewWorldSeeded builds a world with an explicit RNG seed.
func NewWorldSeeded(city *City, cfg *CityConfig, seed uint64) (*World, error) {
	// Apply pacing defaults here as well as in the loader, so a config built in
	// code (tests, the harness, an API caller) behaves the same as one read
	// from disk instead of silently running with zeroed multipliers.
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	w := &World{
		City:           city,
		Speed:          1,
		carClasses:     cfg.CarClasses,
		Ledger:         NewLedger(cfg.StartMoney),
		Seed:           seed,
		ordersCfg:      cfg.Orders,
		charging:       cfg.Charging,
		achievements:   append([]AchievementConfig(nil), cfg.Achievements...),
		unlocks:        append([]UnlockConfig(nil), cfg.Unlocks...),
		builds:         append([]BuildConfig(nil), cfg.Builds...),
		built:          map[string]bool{},
		permitted:      map[string]bool{},
		buyableClasses: map[string]bool{},
	}
	w.spawn.NextEligibleTick = map[string]int64{}

	// Deterministic car ordering: the sim and every replay depend on it.
	for i, ic := range cfg.InitialCars {
		pos := ic.Pos
		if !city.Walkable(pos) {
			return nil, fmt.Errorf("initial car %d at %s is not on walkable terrain", i, pos)
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
	return w, nil
}

// Car returns the car with the given id.
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

// Advance runs exactly one tick and returns the events it produced.
//
// The order is fixed so a replay of (seed, commands) reproduces the world:
//  1. resolve each car's queued move, in car id order
//  2. resolve pickups/dropoffs for cars that landed on a kitchen or customer
//  3. charge cars standing on a charging tile
//  4. spawn due orders
//  5. expire overdue orders
//  6. stamp the tick counter
func (w *World) Advance() []Event {
	if w.Paused {
		return nil
	}
	// Events are captured as they are emitted rather than sliced off the log at
	// the end: the log is trimmed to MaxEvents, and a slice of the trimmed log
	// silently returned nothing once the trim point passed the tick's first
	// event.
	w.currentTickEvents = w.currentTickEvents[:0]

	// 1. Moves. Sorting by id (not slice order) makes the result independent
	// of how cars were appended, so buying a car never changes how existing
	// cars behave on the same tick.
	ordered := make([]*Car, len(w.Cars))
	copy(ordered, w.Cars)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	moved := make([]*Car, 0, len(ordered))
	for _, car := range ordered {
		if w.stepCar(car) {
			moved = append(moved, car)
		}
	}

	// 2. Pickups and dropoffs, only for cars that actually moved. A car parked
	// on a kitchen with a freshly assigned order collects on its next step,
	// not by standing still — otherwise assignment alone would complete work.
	for _, car := range moved {
		w.resolveTileInteractions(car)
	}

	// 3. Charging. A car on a charging tile regains charge over ticks rather
	// than instantly, which is what forces planning instead of teleporting
	// home for free.
	for _, car := range ordered {
		w.chargeCar(car)
	}

	// 4-5. Order lifecycle.
	w.spawnOrders()
	w.expireOrders()

	// 6. Progression: achievements and unlocks are evaluated once per tick on
	// the settled world, so a delivery that completes a requirement is
	// reflected on the same tick.
	w.evalProgression()

	w.Tick++
	return append([]Event(nil), w.currentTickEvents...)
}

// stepCar attempts the car's queued move for this tick. It reports whether the
// car actually changed position, which is what decides if tile interactions
// run.
func (w *World) stepCar(car *Car) bool {
	dir := car.QueuedMove
	car.QueuedMove = ""
	if dir == "" {
		return false
	}

	if car.Battery <= 0 {
		car.LastReject = RejectNoBattery
		w.emit(EventBlocked, car, fmt.Sprintf("%s has no battery left", car.Name))
		return false
	}

	next, ok := car.Pos.Neighbour(dir)
	if !ok {
		car.LastReject = RejectBadDirection
		return false
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
			return false
		}
		car.LastReject = RejectBlocked
		w.emit(EventBlocked, car,
			fmt.Sprintf("%s cannot go %s: %s", car.Name, dir, w.blockReason(next, t)))
		return false
	}

	cost := w.City.WalkCost(next, car.Class)
	if car.Battery < cost {
		// Not enough charge to enter this tile. The car stays put; it is not
		// stranded yet unless it has no charge at all.
		car.LastReject = RejectNoBattery
		w.emit(EventBlocked, car,
			fmt.Sprintf("%s needs %d battery for %s, has %d", car.Name, cost, dir, car.Battery))
		return false
	}

	car.Pos = next
	car.Battery -= cost
	car.StepsTaken++
	car.LastReject = RejectNone
	w.emit(EventMoved, car, fmt.Sprintf("%s moved %s", car.Name, dir))

	if car.Battery <= 0 && !w.City.ChargingAt(car.Pos) {
		w.emit(EventStranded, car, fmt.Sprintf("%s is out of battery at %s", car.Name, car.Pos))
	}
	return true
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
// tile.
func (w *World) chargeCar(car *Car) {
	if !w.City.ChargingAt(car.Pos) || car.Full() {
		return
	}
	before := car.Battery
	car.Battery = min(car.Battery+w.chargeRate(car.Pos), car.Class.MaxBattery)
	if car.Battery != before {
		w.emit(EventCharged, car,
			fmt.Sprintf("%s charging %d/%d", car.Name, car.Battery, car.Class.MaxBattery))
	}
}

// chargeRate is how much battery a tile restores per tick. A depot is a fast
// charger; a standalone charger is slower. Rates come from the city config so
// pacing can be tuned without a code change.
func (w *World) chargeRate(p Position) int {
	t, ok := w.City.At(p)
	if !ok {
		return 0
	}
	if t == DepotTile {
		if w.charging.DepotRate > 0 {
			return w.charging.DepotRate
		}
		return DefaultDepotChargeRate
	}
	if w.charging.ChargerRate > 0 {
		return w.charging.ChargerRate
	}
	return DefaultChargerChargeRate
}

// Charge-rate fallbacks, used when a city file omits the charging block. They
// match maps/city.json, so an in-code config and the shipped map behave
// identically — the loader and NewWorld both apply these.
const (
	DefaultDepotChargeRate   = 6
	DefaultChargerChargeRate = 2
)

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
	parked := 0
	for _, c := range w.Cars {
		if c.Pos == depot.Pos {
			parked++
		}
	}
	if parked >= depot.Capacity {
		return nil, fmt.Errorf("depot %q is full (%d/%d)", depotID, parked, depot.Capacity)
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
	w.Ledger.Money -= cc.Price
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

// Tow returns a stranded car to its nearest depot for a fee-free reset in P1.
// P2 adds the fee; the shape of the operation is already here.
func (w *World) Tow(carID string) error {
	car, ok := w.Car(carID)
	if !ok {
		return fmt.Errorf("no car %q", carID)
	}
	if len(w.City.Depots) == 0 {
		return fmt.Errorf("no depot to tow to")
	}
	// Nearest depot by walking distance, falling back to the first depot if
	// the car cannot reach any (which is exactly the stranded case).
	dest := w.City.Depots[0]
	best := -1
	for _, d := range w.City.Depots {
		if dist := w.City.Distance(car.Pos, d.Pos); dist >= 0 && (best < 0 || dist < best) {
			best = dist
			dest = d
		}
	}
	car.Pos = dest.Pos
	car.Battery = 1 // towed in, not repaired: it must charge up properly
	car.QueuedMove = ""
	car.LastReject = RejectNone
	w.Ledger.Tows++
	w.emit(EventCharged, car, fmt.Sprintf("%s towed to %s", car.Name, dest.ID))
	return nil
}

// emit appends an event at the current tick.
//
// Events are kept for client feeds, but a session can run for hundreds of
// thousands of ticks, so the log is trimmed to the most recent MaxEvents. Only
// the tail is ever useful: an agent polls for what changed since its last read.
func (w *World) emit(kind string, car *Car, msg string) {
	ev := Event{Tick: w.Tick, Type: kind, Message: msg}
	if car != nil {
		ev.CarID = car.ID
		ev.Pos = car.Pos
	}
	w.Events = append(w.Events, ev)
	w.currentTickEvents = append(w.currentTickEvents, ev)
	if len(w.Events) > MaxEvents {
		// Copy down rather than reslice, so the backing array does not grow
		// without bound across a long session.
		keep := len(w.Events) - MaxEvents
		copy(w.Events, w.Events[keep:])
		w.Events = w.Events[:MaxEvents]
	}
}

// MaxEvents bounds the retained event log.
const MaxEvents = 500

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

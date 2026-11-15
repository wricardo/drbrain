package engine

import (
	"fmt"
	"sort"
)

// Road closures.
//
// A closure takes a stretch of street out of play for a while: maintenance, a
// burst pipe, a parade. It is the mechanic the Manhattan layout exists to make
// possible -- on the old open-plaza map there was nothing to close.
//
// Three design rules, each of which is a bug that was worth avoiding:
//
//  1. CLOSURES ARE DERIVED, NEVER STORED. A closure is a pure function of
//     (seed, tick, chapter), exactly like order jitter. That means a save needs
//     no closure state at all, and a saved game replays the same street works
//     on the same ticks. Writing closures into the terrain would bake them into
//     the save and make them permanent, which is the opposite of the point.
//
//  2. A CLOSURE MUST NEVER DISCONNECT ANYTHING. Every candidate is checked
//     against a reachability guard before it is allowed to start. A closure
//     that would cut a kitchen or customer off from the fleet is silently
//     skipped and the next candidate is tried.
//
//     The guard is measured from DEPOTS ONLY (see reachFromSeeds). That
//     distinction is load-bearing: seeding from chargers as well let a closure
//     sever the harbour from the only depot on every run, because the harbour
//     carries its own charger once b04_harbour_gate exists.
//
//     The guarantee is per-candidate, so it is complete only while
//     MaxConcurrent is 1. Above that, two individually-safe runs can jointly
//     disconnect a pocket; see the MaxConcurrent field comment.
//
//  3. A CLOSURE IS NOT A WALL. A car standing on a closed tile may drive off
//     it; it just cannot drive back on. Being trapped by a scheduled event the
//     player did not choose is not a decision, it is a bug with a timer.

// ClosureConfig is the pacing table for street works.
//
// The zero value means "no closures", so a map that does not want chaos does
// not have to opt out.
type ClosureConfig struct {
	// Enabled turns the whole system on. Every other field is ignored when it
	// is false, so a map can keep a tuned table and switch it off.
	Enabled bool `json:"enabled"`

	// FromChapter is the first chapter (builds completed) at which closures
	// may start. The early game must stay quiet: a player with one scooter and
	// no money has no interesting answer to a detour.
	FromChapter int `json:"from_chapter"`

	// EveryTicks is the base gap between closures. Each closure lasts
	// DurationTicks, so the street works are on the road roughly
	// Duration/Every of the time.
	EveryTicks    int `json:"every_ticks"`
	DurationTicks int `json:"duration_ticks"`

	// MaxConcurrent caps how many closures can be live at once. At 1 (what
	// city.json ships) a single closure is checked in isolation, which the
	// geometry makes safe. Above 1 that reasoning does NOT hold: the guard
	// validates each candidate against the CURRENT grid and never against a
	// sibling that is already live, so two individually-safe runs can jointly
	// cut a pocket off. Measured on the shipped map, every one of the 182
	// candidates passes closureIsSafe on its own, and the pair
	// [(3,0)(4,0)(5,0)] + [(0,3)(1,3)(2,3)] disconnects 5 tiles from the depot
	// (5 is the worst found; the milder [(0,0)(1,0)(2,0)] pair still loses 2).
	//
	// Raising this is NOT sufficient to cause that, which is worth knowing
	// before anyone treats a value of 2 as a live hazard: a second closure also
	// needs to be due while the first is still running, so overlap requires
	// DurationTicks >= EveryTicks as well. Shipped values (260 vs 900) can never
	// overlap, and forcing MaxConcurrent to 2 over 6000 ticks produced no
	// overlapping pair and no lost tile. Do both of the following before
	// enabling it: validate the union rather than the candidate, and make the
	// pacing coherent.
	MaxConcurrent int `json:"max_concurrent"`

	// SegmentTiles is how many tiles a closure takes out, laid as a straight
	// run along one street.
	SegmentTiles int `json:"segment_tiles"`
}

// Closure is one stretch of closed street.
type Closure struct {
	ID    string
	Tiles []Position

	StartTick int64
	EndTick   int64

	// Reason is flavour for the event feed. It has no mechanical effect.
	Reason string
}

// ActiveAt reports whether the closure is live at the given tick.
//
// The window is half-open, StartTick <= tick < EndTick: the street is closed ON
// its start tick and already REOPENED on its end tick. Verified on a 10..20
// closure — 9 false, 10 true, 19 true, 20 false.
//
// So EndTick is the first tick a car may drive through again, not the last tick
// it may not. A closure drawn on the map for exactly EndTick-StartTick ticks
// reports active on precisely that many, which is the arithmetic the renderer
// relies on when it prints a countdown.
func (c Closure) ActiveAt(tick int64) bool {
	return tick >= c.StartTick && tick < c.EndTick
}

// Contains reports whether a tile is inside the closure.
func (c Closure) Contains(p Position) bool {
	for _, t := range c.Tiles {
		if t == p {
			return true
		}
	}
	return false
}

// closureReasons are picked from deterministically so the feed reads like a
// city rather than like a random number generator.
var closureReasons = []string{
	"water main repair",
	"resurfacing works",
	"road collapse",
	"gas leak",
	"festival barriers",
	"crane delivery",
}

// closureAt derives the closure that would start on this tick, if any.
//
// It is a pure function of (seed, tick, chapter): no state, no RNG stream, so
// the same save replays the same street works.
func (w *World) closureAt(tick int64) (Closure, bool) {
	cfg := w.closuresCfg
	if !cfg.Enabled {
		return Closure{}, false
	}
	if w.Chapter() < cfg.FromChapter {
		return Closure{}, false
	}
	if cfg.EveryTicks <= 0 || cfg.DurationTicks <= 0 {
		return Closure{}, false
	}
	// Start on a tick that is a multiple of EveryTicks, offset by a seeded
	// jitter so two sessions do not close the same street on the same tick.
	phase := int64(Roll(w.Seed, 0, "closure-phase", cfg.EveryTicks))
	if tick < phase || (tick-phase)%int64(cfg.EveryTicks) != 0 {
		return Closure{}, false
	}
	return w.pickClosure(tick, cfg)
}

// pickClosure chooses a segment for a closure starting at tick.
//
// Candidates are enumerated in a deterministic order (row-major over street
// runs), then one is drawn by seeded roll, then validated. A rejected
// candidate advances to the next index rather than re-rolling, so a tick can
// never fail to make progress because of an unlucky draw.
func (w *World) pickClosure(tick int64, cfg ClosureConfig) (Closure, bool) {
	span := cfg.SegmentTiles
	if span <= 0 {
		span = 3
	}
	candidates := w.closureCandidates(span)
	if len(candidates) == 0 {
		return Closure{}, false
	}
	start := Roll(w.Seed, tick, "closure-pick", len(candidates))
	for i := range candidates {
		tiles := candidates[(start+i)%len(candidates)]
		if !w.closureIsSafe(tiles) {
			continue
		}
		reason := Pick(w.Seed, tick, "closure-reason", closureReasons)
		return Closure{
			ID:        fmt.Sprintf("cl_%d", tick),
			Tiles:     tiles,
			StartTick: tick,
			EndTick:   tick + int64(cfg.DurationTicks),
			Reason:    reason,
		}, true
	}
	return Closure{}, false
}

// closureCandidates enumerates every straight run of span walkable tiles along
// a street, in a deterministic order.
//
// Straight runs only: an L-shaped closure is hard to read on the map and hard
// to explain in the feed. Horizontal runs come before vertical ones, and each
// kind is sorted by (y, x), so the enumeration does not depend on map order.
func (w *World) closureCandidates(span int) [][]Position {
	if span <= 0 || w.City == nil {
		return nil
	}
	city := w.City
	var out [][]Position

	addRun := func(tiles []Position) {
		for _, t := range tiles {
			if !city.Walkable(t) {
				return
			}
		}
		// Never close a tile that holds an entity: the pickup or dropoff
		// would become unusable and the closure would read as a broken map
		// rather than as traffic.
		for _, t := range tiles {
			if city.hasEntityAt(t) {
				return
			}
		}
		// Never close a charging tile: stranding a car is not a routing
		// decision the player can answer.
		for _, t := range tiles {
			if city.ChargingAt(t) {
				return
			}
		}
		cp := make([]Position, len(tiles))
		copy(cp, tiles)
		out = append(out, cp)
	}

	for y := range city.GridSize {
		for x := 0; x+span <= city.GridSize; x++ {
			run := make([]Position, 0, span)
			for i := range span {
				run = append(run, Position{x + i, y})
			}
			addRun(run)
		}
	}
	for x := range city.GridSize {
		for y := 0; y+span <= city.GridSize; y++ {
			run := make([]Position, 0, span)
			for i := range span {
				run = append(run, Position{x, y + i})
			}
			addRun(run)
		}
	}
	return out
}

// closureIsSafe reports whether closing these tiles leaves the city coherent.
//
// Two tests, both from the depot set:
//
//  1. Nothing that is reachable now may become unreachable. Stricter than
//     "nothing is stranded": a closure that shuts an empty cul-de-sac leaves a
//     car parked there with no way out, which the entity guard would never
//     notice.
//  2. No kitchen or customer may lose its route to the fleet.
//
// SCOPE: it validates ONE candidate against the grid as it stands, and does not
// consider closures that are already live. Two candidates that each pass can
// still disconnect a pocket between them, so this guarantee is only complete
// while MaxConcurrent is 1. See the MaxConcurrent field comment for the measured
// counter-example.
func (w *World) closureIsSafe(tiles []Position) bool {
	city := w.City
	blocked := make(map[Position]bool, len(tiles))
	for _, t := range tiles {
		if !city.Walkable(t) {
			return false
		}
		blocked[t] = true
	}

	// Every tile that is reachable now must still be reachable after. This is
	// stricter than "nothing is stranded": a closure that cuts off an empty
	// cul-de-sac leaves a car parked there with no way out, which the entity
	// guard would never notice.
	reachBefore := w.reachFromSeeds(nil)
	reachAfter := w.reachFromSeeds(blocked)

	// Tiles inside the closure are exempt: they are the ones that may not be
	// entered, and a closed street is expected to be unreachable.
	for p := range reachBefore {
		if blocked[p] {
			continue
		}
		if !reachAfter[p] {
			return false
		}
	}

	// Entities on the far side of a closure must not lose their route.
	for _, k := range city.Kitchens {
		if city.IsHidden(k.Pos) || blocked[k.Pos] {
			continue
		}
		if reachBefore[k.Pos] && !reachAfter[k.Pos] {
			return false
		}
	}
	for _, cu := range city.Customers {
		if city.IsHidden(cu.Pos) || blocked[cu.Pos] {
			continue
		}
		if reachBefore[cu.Pos] && !reachAfter[cu.Pos] {
			return false
		}
	}
	return true
}

// reachFromSeeds is BFS from every DEPOT, optionally treating some tiles as
// impassable.
//
// Depots only, deliberately -- and this is the one place it must differ from
// City.StrandedEntities, which seeds from depots AND chargers because a build
// may legitimately rely on a charger. A closure may not: the fleet starts at a
// depot and a car cannot teleport to a charger, so a charger is somewhere to
// drive TO, never a place a severed district can be served FROM.
//
// Seeding from chargers as well was a real defect, not a theory. Once
// b04_harbour_gate adds ch_harbour_gate, the harbour contains its own seed, so a
// closure across the harbour road passed the guard while leaving 59 tiles,
// k_sushi, c_docks, c_lighthouse and 5 of 8 cars unreachable from the only
// depot -- every run, at tick 5096 from the default seed, for 260 ticks.
//
// Chargers stay PASSABLE here: reaching one is still a valid route.
func (w *World) reachFromSeeds(blocked map[Position]bool) map[Position]bool {
	city := w.City
	reach := map[Position]bool{}
	var queue []Position

	push := func(p Position) {
		if reach[p] || blocked[p] || !city.Walkable(p) {
			return
		}
		reach[p] = true
		queue = append(queue, p)
	}
	for _, d := range city.Depots {
		push(d.Pos)
	}

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, dir := range AllDirections {
			n, ok := p.Neighbour(dir)
			if !ok || reach[n] || blocked[n] || !city.Walkable(n) {
				continue
			}
			reach[n] = true
			queue = append(queue, n)
		}
	}
	return reach
}

// hasEntityAt reports whether a kitchen, customer, depot or charger sits on p.
func (c *City) hasEntityAt(p Position) bool {
	for _, k := range c.Kitchens {
		if k.Pos == p {
			return true
		}
	}
	for _, cu := range c.Customers {
		if cu.Pos == p {
			return true
		}
	}
	for _, d := range c.Depots {
		if d.Pos == p {
			return true
		}
	}
	for _, ch := range c.Chargers {
		if ch.Pos == p {
			return true
		}
	}
	return false
}

// ActiveClosures returns the closures live at the current tick, in a stable
// order. This is what the renderer, the movement rule and the snapshot all
// read, so there is exactly one definition of "closed right now".
func (w *World) ActiveClosures() []Closure {
	cfg := w.closuresCfg
	if !cfg.Enabled {
		return nil
	}
	var out []Closure
	for _, c := range w.closures {
		if c.ActiveAt(w.Tick) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClosedAt reports whether a tile is closed by street works right now.
func (w *World) ClosedAt(p Position) bool {
	for _, c := range w.closures {
		if c.ActiveAt(w.Tick) && c.Contains(p) {
			return true
		}
	}
	return false
}

// ClosedSet is the set of tiles closed right now, for renderers and for
// callers that need to test many tiles without a scan per tile.
//
// It returns NIL when nothing is closed (measured), not an empty map, and that
// is deliberate: it keeps the common case allocation-free, and a nil map is
// valid for reads, so `if closed[p]` works either way. What it is NOT safe for
// is writing — `closed[p] = true` on the no-closure path panics with
// "assignment to entry in nil map". A caller that builds on top of it must
// allocate its own map or use a read-only containment check.
func (w *World) ClosedSet() map[Position]bool {
	cl := w.ActiveClosures()
	if len(cl) == 0 {
		return nil
	}
	out := map[Position]bool{}
	for _, c := range cl {
		for _, p := range c.Tiles {
			out[p] = true
		}
	}
	return out
}

// blockReasonForCar explains why a car cannot enter a tile, including the
// closure rule. Hidden is checked before terrain because a hidden tile's
// terrain is a secret: reporting "water" for a fogged tile would leak the map.
func (w *World) blockReasonForCar(p Position, t CellType, car *Car) string {
	if w.City.IsHidden(p) {
		return "unexplored"
	}
	if w.ClosedAt(p) && !(car != nil && car.Pos == p) {
		return "closed for works"
	}
	if !t.Passable() {
		return string(t)
	}
	return "unreachable"
}

// stepClosures starts and retires scheduled closures for this tick.
//
// Starting is derived (see closureAt); retiring is just expiry. The event feed
// gets one entry for each direction so a player watching the feed can see why
// their usual route stopped being their usual route.
func (w *World) stepClosures() {
	cfg := w.closuresCfg
	if !cfg.Enabled {
		return
	}

	// Retire finished closures first, so the concurrency cap reflects reality.
	kept := w.closures[:0]
	for _, c := range w.closures {
		if c.ActiveAt(w.Tick) {
			kept = append(kept, c)
			continue
		}
		w.emit(EventClosureEnd, nil, fmt.Sprintf("%s reopened (%s)", closureName(c), c.Reason))
	}
	w.closures = kept

	if c, ok := w.closureAt(w.Tick); ok {
		if cap := cfg.MaxConcurrent; cap > 0 && len(w.closures) >= cap {
			return
		}
		w.closures = append(w.closures, c)
		w.emit(EventClosure, nil, fmt.Sprintf("%s closed (%s) until tick %d",
			closureName(c), c.Reason, c.EndTick))
	}
}

// routeDistance is the walking distance that respects anything currently
// blocking the streets: street works, and nothing else today.
//
// Every deadline, nearest-kitchen query and reward distance goes through this
// rather than City.Distance, so a closure that lengthens a route lengthens the
// deadline with it. Quoting a deadline computed from the pre-closure route is
// how a player gets punished for traffic they did not create.
//
// It returns -1 only when no route exists over walkable terrain. A closed ORIGIN
// is NOT impossible: a closure blocks entry, and a car already standing on a
// closed tile may drive off, so DistanceAvoiding treats the origin as passable.
// Keeping those two rules aligned is what stops a closure that happens under a
// car from shortening the deadline it was already quoted (measured 16 ticks
// either side; this was `drbrain-delivery-bic`).
//
// Callers that sum legs should still handle -1 explicitly rather than adding it,
// because -1 means "no route", not "zero distance".
func (w *World) routeDistance(from, to Position) int {
	if len(w.closures) == 0 {
		return w.City.Distance(from, to)
	}
	return w.City.DistanceAvoiding(from, to, w.ClosedAt)
}

// RouteDistance is the walking distance a car would actually drive, taking
// street works into account. Callers outside the engine (the harness, an agent)
// need this rather than City.Distance, or they plan through closed streets.
//
// It ignores other cars, deliberately. A move into a full tile is refused but
// the queued direction is kept, so traffic costs a car ticks and never battery —
// the route is the same length either way. Pricing a detour around cars instead
// made every route look unaffordable and the fleet charged forever (measured
// 79 deliveries against 250, see the harness's nextStepToward).
//
// A car standing on a closed tile still gets a real distance, because the
// closure blocks entry rather than occupancy — verified: from a closed depot
// tile this returns 3 for a reachable customer, not -1. -1 means genuinely
// unreachable, so a planner may treat it as "no route".
//
// RouteWalkable is the odd one out and deliberately so: it answers "may a car
// ENTER here", which is still false for a closed tile even though the car on it
// can leave.
func (w *World) RouteDistance(from, to Position) int {
	return w.routeDistance(from, to)
}

// RouteWalkable reports whether a car may enter a tile, considering closures as
// well as terrain. Note this is about ENTRY, not occupancy: a car standing on a
// tile that closes under it can still leave.
func (w *World) RouteWalkable(p Position) bool {
	return w.City.Walkable(p) && !w.ClosedAt(p)
}

// StepToward returns the first single step on a shortest route from here to
// there, or "" when there is no route.
//
// It is closure-aware: it filters the neighbours it expands, so a closed tile is
// never entered, and it never tests the ORIGIN — a car standing on a tile that
// closed under it must still be able to drive off. RouteDistance now applies the
// same rule (a closure blocks entry, not occupancy), so the two agree on that
// case: measured from a closed depot tile, RouteDistance returns a real distance
// and StepToward a real direction, and the car moves.
//
// It exists for tooling and tests. It is NOT a player-facing autopilot: the
// game's rule is that the player queues every step (IDEA.txt D1), and nothing
// in the tick loop calls this.
//
// From == to returns "" rather than a direction, so "" is ambiguous between
// "already there" and "no route" — check the positions first if that matters.
//
// It plans through cars, deliberately. A move into a full tile is refused but the
// queued direction is kept, so a driver re-planning the same route every tick
// simply waits for the tile to clear — it does not stall forever, and detouring
// around traffic costs battery that traffic never costs. Measured three ways on
// the shipped map over 3000 ticks: waiting delivered 250, detouring while pricing
// the detour delivered 79, and detouring while pricing the straight route
// delivered 198 with 84 rescues. See cmd/drbrain's nextStepToward.
func (w *World) StepToward(from, to Position) Direction {
	if from == to {
		return ""
	}
	type node struct {
		p     Position
		first Direction
	}
	seen := map[Position]bool{from: true}
	queue := []node{{p: from}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := cur.p.Neighbour(d)
			if !ok || seen[n] || !w.RouteWalkable(n) {
				continue
			}
			first := cur.first
			if cur.p == from {
				first = d
			}
			if n == to {
				return first
			}
			seen[n] = true
			queue = append(queue, node{p: n, first: first})
		}
	}
	return ""
}

// NearestChargers returns charging tiles reachable from start over open
// streets, nearest first.
//
// Two behaviours a caller will otherwise get wrong:
//
//   - The start tile is INCLUDED when it charges (distance 0). Measured from the
//     depot, which is a charging tile: [(3,3) (9,9)] — (3,3) is where we asked
//     from. So "nearest charger" answers "where should I go to charge", and a
//     caller that wants "somewhere else to charge" must skip results equal to
//     start.
//   - limit <= 0 means NO limit, not "none". Measured: limit 0 returns the full
//     list and limit 1 returns exactly one. A caller passing a computed budget of
//     zero silently gets everything.
//
// Ties break by Y then X, so the order is stable across calls and maps do not
// reshuffle the answer.
func (w *World) NearestChargers(start Position, limit int) []Position {
	if len(w.closures) == 0 {
		return w.City.NearestChargingTiles(start, limit)
	}
	dist := map[Position]int{start: 0}
	queue := []Position{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok {
				continue
			}
			if _, seen := dist[n]; seen {
				continue
			}
			if !w.RouteWalkable(n) {
				continue
			}
			dist[n] = dist[p] + 1
			queue = append(queue, n)
		}
	}
	type entry struct {
		p Position
		d int
	}
	var entries []entry
	for p, d := range dist {
		if w.City.ChargingAt(p) {
			entries = append(entries, entry{p, d})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.d != b.d {
			return a.d < b.d
		}
		if a.p.Y != b.p.Y {
			return a.p.Y < b.p.Y
		}
		return a.p.X < b.p.X
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]Position, len(entries))
	for i, e := range entries {
		out[i] = e.p
	}
	return out
}

// closureName renders a closure as a human-readable place for the feed.
func closureName(c Closure) string {
	if len(c.Tiles) == 0 {
		return "road"
	}
	first := c.Tiles[0]
	return fmt.Sprintf("street at %s", first)
}

// rebuildClosures refills the derived closure cache for the current tick.
//
// closures is a cache of what has been announced, not saved state, so a reload
// has to re-derive it. Without this, a save taken during street works reloaded
// with the street open and the two worlds diverged on the very next tick --
// which broke the documented promise that a save needs no closure state.
//
// It walks back the closure window rather than replaying from tick 0: a closure
// that is live at tick T started at some tick in (T-duration, T], and EveryTicks
// bounds how many starts can fall there. The walk is therefore O(duration/
// every + 1) calls to closureAt, not O(T).
//
// Only the START tick is consulted, so this reconstructs exactly what the tick
// loop would have announced. Candidates are re-validated by closureAt as usual,
// so a save reloaded against a changed city.json stays coherent.
func (w *World) rebuildClosures() {
	cfg := w.closuresCfg
	w.closures = nil
	if !cfg.Enabled || cfg.DurationTicks <= 0 || cfg.EveryTicks <= 0 {
		return
	}
	// Anything that could still be live at w.Tick started in this window.
	earliest := w.Tick - int64(cfg.DurationTicks) + 1
	if earliest < 0 {
		earliest = 0
	}
	// Align to the phase the scheduler uses, so we do not miss the first start.
	phase := int64(Roll(w.Seed, 0, "closure-phase", cfg.EveryTicks))
	for start := earliest; start <= w.Tick; start++ {
		if start < phase || (start-phase)%int64(cfg.EveryTicks) != 0 {
			continue
		}
		c, ok := w.closureAt(start)
		if !ok {
			continue
		}
		if c.ActiveAt(w.Tick) {
			w.closures = append(w.closures, c)
		}
	}
	sort.Slice(w.closures, func(i, j int) bool { return w.closures[i].ID < w.closures[j].ID })
}

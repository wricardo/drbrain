package main

import (
	"container/heap"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// cmdPerfect is the best-effort planner (`drbrain perfect`): the same engine and
// the same public API as the pacing harness in sim.go, but a driver that plans
// instead of reacting. It exists to answer "how good can a run get?", so the
// pacing numbers can be read against a ceiling rather than against themselves.
//
// What makes it different from the pacing bot, which keeps no state between
// ticks and therefore cannot commit to anything:
//
//   - Every car owns an ordered list of stops (pickups, dropoffs, charges) and a
//     committed route of future positions. Cars are not asked "where now?" every
//     tick; they follow a plan until it is done or the world rejects it.
//   - Routes are planned in space and time against every other car's committed
//     route, so two cars never want the same one-car tile at the same tick and a
//     head-on swap (which the engine refuses) is never planned. A car that must
//     wait is given a real wait, and a car in someone's way is given a way out.
//   - Orders are inserted into the cheapest position of the cheapest car's stop
//     list after simulating the whole tour: legs in ticks, battery in cost units
//     (a ground tile costs more than a road tile, which the step count does not
//     show), charge detours where the battery would otherwise strand the car,
//     hold space, and every order's deadline. An order no tour can serve in time
//     is left alone rather than assigned and abandoned.
//   - Idle cars park on the depot, which holds the whole fleet, instead of
//     sleeping in a one-lane street.
//
// It is still an online player: it sees an order when the engine raises it. So
// it is a ceiling for planning, not an oracle, and orders the world cannot be
// served by any affordable fleet in their window will still expire; the summary
// says which expiries those are.
func cmdPerfect(args []string) int {
	handBack := false
	opts := &simOptions{path: "maps/city.json", ticks: 3000, every: 200, seed: 0x5eed5eed}
	verbose := false
	fs := flag.NewFlagSet("perfect", flag.ContinueOnError)
	fs.StringVar(&opts.path, "f", opts.path, "path to city file")
	fs.IntVar(&opts.ticks, "ticks", opts.ticks, "number of ticks to simulate (must be > 0)")
	fs.IntVar(&opts.every, "every", opts.every, "print a stats row every N ticks (must be > 0)")
	fs.Uint64Var(&opts.seed, "seed", opts.seed, "RNG seed")
	fs.BoolVar(&verbose, "v", false, "log purchases and assignments")
	fs.BoolVar(&handBack, "handback", false, "let cars hand back uncollected orders that would arrive late (re-quotes deadlines)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "perfect: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "perfect: %v\n", err)
		return 2
	}

	city, cfg, err := engine.CityFromFile(opts.path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	world, err := engine.NewWorldSeeded(city, cfg, opts.seed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	pl := newPilot(world, cfg, verbose)
	pl.handBack = handBack
	fmt.Printf("perfect: %s, seed %#x, %d ticks, stats every %d\n\n", cfg.Name, opts.seed, opts.ticks, opts.every)
	fmt.Printf("%6s %5s %5s %6s %5s %7s %7s %7s %6s %4s %7s\n",
		"tick", "open", "tgt", "deliv", "late", "expired", "strand", "money", "rep", "cars", "blocked")
	for range opts.ticks {
		pl.step()
		pl.after(world.Advance())
		if world.Tick%int64(opts.every) == 0 {
			pl.row()
		}
	}
	fmt.Println()
	pl.row()
	fmt.Println()
	pl.summary()
	return 0
}

// --- plan data ---------------------------------------------------------------

type P = engine.Position

const (
	// horizon is how many ticks ahead a route is planned. A leg that cannot be
	// planned inside it is retried next tick rather than planned loosely.
	horizon = 90
	// legSlack is the ticks added to every leg in tour estimates, for the
	// traffic the estimate does not see.
	legSlack = 2
	// battMargin is the battery units kept beyond the route's own cost: the
	// shortest-time path is not always the cheapest in battery.
	battMargin = 2
	// evadeReserve is the battery a car keeps back when stepping out of the way.
	evadeReserve = 4
	// noCharger is the reserve demanded of a stop no charger can be reached from.
	noCharger = 1 << 20
	// fleetSlack is how many cars above the demand target the fleet may grow.
	fleetSlack = 3
	// surplusCash is the balance above which spare money buys a surplusClass car.
	surplusCash  = 3000
	surplusClass = "van"
	// slackCap is the deadline slack, in ticks, past which a tour is not
	// preferred any further for having more.
	slackCap = 6
	// handBackMargin is the ticks of slack below which a car hands back an
	// order it has not collected yet.
	handBackMargin = 2
	// needSlack is how far past fleetSlack a car bought for a specific order may go.
	needSlack = 3
	// cashReserve is held back from every purchase.
	cashReserve = 50
	// chargerEvery is how often (ticks) a charger purchase is considered, and
	// chargerCash the balance that must remain after it.
	chargerEvery = 20
	chargerCash  = 1500
	// chargerMinGain is the least total distance cut that justifies a charger.
	chargerMinGain = 40
)

type stopKind int

const (
	skPickup stopKind = iota
	skDropoff
	skCharge
	skPark
)

type stop struct {
	kind  stopKind
	pos   P
	order string
	// chargeTo is the battery a charge stop waits for.
	chargeTo int
	// reserve is the battery that must remain after arriving, so the car can
	// still reach a charger afterwards.
	reserve int
}

type driver struct {
	car *engine.Car
	// base is the work, in service order: pickups and dropoffs only. Everything
	// else (charges, parking) is derived from it and the car's present state.
	base []stop
	// route is the committed future: route[0] is where the car is next tick.
	// A wait repeats the current position.
	route []P
	// stayUntil is the last tick a charging car is expected to hold its tile.
	stayUntil int64
	// commit is a charge the car has decided on and is driving to or sitting on.
	// Deciding afresh every tick from the battery flickers: one unit gained on
	// the charger said "enough", the step off it cost the unit back and said
	// "charge", and the car shuttled between two tiles forever.
	commit *stop
}

type spawnInfo struct {
	tick     int64
	boxes    int
	bestCap  int
	assigned bool
	// assignTick, predicted and car record the last assignment, so an expiry can
	// be compared with the arrival the planner promised.
	assignTick, predicted int64
	car                   string
}

type pilot struct {
	w       *engine.World
	cfg     *engine.CityConfig
	n       int
	verbose bool
	// handBack lets a car return an order it has not collected to the queue (see
	// rebalance). Off by default: the engine re-quotes an order's deadline, and
	// only ever extends it, every time it is assigned, so handing back and
	// reassigning buys extra time and flatters the expiry count.
	handBack bool

	drivers []*driver
	byCar   map[string]*driver
	dmCache map[dmKey]*dmap

	kitchens  map[string]P
	customers map[string]P

	blocked  int
	replans  int
	bought   int
	seen     map[string]*spawnInfo
	expiries map[string]int
	counted  map[string]bool
}

func newPilot(w *engine.World, cfg *engine.CityConfig, verbose bool) *pilot {
	return &pilot{
		w: w, cfg: cfg, n: w.City.GridSize, verbose: verbose,
		byCar:    map[string]*driver{},
		seen:     map[string]*spawnInfo{},
		expiries: map[string]int{},
		counted:  map[string]bool{},
	}
}

func (pl *pilot) idx(p P) int { return p.Y*pl.n + p.X }

func (pl *pilot) inGrid(p P) bool { return p.X >= 0 && p.Y >= 0 && p.X < pl.n && p.Y < pl.n }

func (pl *pilot) logf(format string, args ...any) {
	if pl.verbose {
		fmt.Printf("  t%-5d %s\n", pl.w.Tick, fmt.Sprintf(format, args...))
	}
}

// --- distances ---------------------------------------------------------------

type dmKey struct {
	target P
	step   int
}

// dmap holds, for every tile, the distance to one target: steps is the fewest
// ticks, batt the fewest battery units (the two paths can differ, because a
// ground tile costs extra battery but no extra time). -1 is unreachable.
type dmap struct{ steps, batt []int }

type dist struct{ c, i int }
type distHeap []dist

func (h distHeap) Len() int           { return len(h) }
func (h distHeap) Less(i, j int) bool { return h[i].c < h[j].c }
func (h distHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *distHeap) Push(x any)        { *h = append(*h, x.(dist)) }
func (h *distHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func (pl *pilot) dm(target P, class engine.CarClassConfig) *dmap {
	key := dmKey{target, class.StepCost}
	if m, ok := pl.dmCache[key]; ok {
		return m
	}
	cells := pl.n * pl.n
	m := &dmap{steps: make([]int, cells), batt: make([]int, cells)}
	for i := range m.steps {
		m.steps[i], m.batt[i] = -1, -1
	}
	const inf = 1 << 30
	if !pl.inGrid(target) {
		pl.dmCache[key] = m
		return m
	}

	// Fewest ticks: breadth-first from the target over tiles a car may enter.
	queue := []P{target}
	m.steps[pl.idx(target)] = 0
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, d := range engine.AllDirections {
			nb, ok := v.Neighbour(d)
			if !ok || !pl.inGrid(nb) || m.steps[pl.idx(nb)] >= 0 || !pl.w.RouteWalkable(nb) {
				continue
			}
			m.steps[pl.idx(nb)] = m.steps[pl.idx(v)] + 1
			queue = append(queue, nb)
		}
	}

	// Fewest battery units: Dijkstra from the target; entering v costs v's walk cost.
	best := make([]int, cells)
	for i := range best {
		best[i] = inf
	}
	best[pl.idx(target)] = 0
	h := &distHeap{{0, pl.idx(target)}}
	for h.Len() > 0 {
		it := heap.Pop(h).(dist)
		if it.c > best[it.i] {
			continue
		}
		v := P{X: it.i % pl.n, Y: it.i / pl.n}
		enter := pl.w.City.WalkCost(v, class)
		for _, d := range engine.AllDirections {
			nb, ok := v.Neighbour(d)
			if !ok || !pl.inGrid(nb) || !pl.w.RouteWalkable(nb) {
				continue
			}
			if c := it.c + enter; c < best[pl.idx(nb)] {
				best[pl.idx(nb)] = c
				heap.Push(h, dist{c, pl.idx(nb)})
			}
		}
	}
	for i, c := range best {
		if c < inf {
			m.batt[i] = c
		}
	}
	// A tile closed for street works cannot be entered but can be left, so a car
	// standing on one (works start under cars) is as far from the target as its
	// best open neighbour, plus a step. Leaving these out reported such a car as
	// cut off from every charger.
	for y := range pl.n {
		for x := range pl.n {
			t := P{X: x, Y: y}
			if pl.w.RouteWalkable(t) || !pl.w.City.Walkable(t) {
				continue
			}
			bs, bb := -1, -1
			for _, d := range engine.AllDirections {
				nb, ok := t.Neighbour(d)
				// Only open neighbours count: chaining through the other closed
				// tiles of the same works made a car inside a three-tile closure
				// look seven steps from a charger it could not leave towards.
				if !ok || !pl.inGrid(nb) || !pl.w.RouteWalkable(nb) || m.steps[pl.idx(nb)] < 0 {
					continue
				}
				if s := m.steps[pl.idx(nb)] + 1; bs < 0 || s < bs {
					bs = s
				}
				if b := m.batt[pl.idx(nb)] + pl.w.City.WalkCost(nb, class); bb < 0 || b < bb {
					bb = b
				}
			}
			m.steps[pl.idx(t)], m.batt[pl.idx(t)] = bs, bb
		}
	}
	pl.dmCache[key] = m
	return m
}

// chargeTiles are the depots and chargers a car can recharge on.
func (pl *pilot) chargeTiles() []P {
	var out []P
	for _, d := range pl.w.City.Depots {
		out = append(out, d.Pos)
	}
	for _, c := range pl.w.City.Chargers {
		out = append(out, c.Pos)
	}
	return out
}

func (pl *pilot) rateAt(p P) int {
	for _, d := range pl.w.City.Depots {
		if d.Pos == p {
			return max(pl.w.City.Charging.DepotRate, 1)
		}
	}
	for _, c := range pl.w.City.Chargers {
		if c.Pos == p {
			return max(c.Rate, 1)
		}
	}
	return 1
}

func (pl *pilot) isDepot(p P) bool {
	for _, d := range pl.w.City.Depots {
		if d.Pos == p {
			return true
		}
	}
	return false
}

// reserveAt is the battery a car needs left on arrival at p to still reach a
// charger: nothing on a charging tile, noCharger when none can be reached.
func (pl *pilot) reserveAt(class engine.CarClassConfig, p P) int {
	if pl.w.City.ChargingAt(p) {
		return 0
	}
	best := noCharger
	for _, ch := range pl.chargeTiles() {
		if b := pl.dm(ch, class).batt[pl.idx(p)]; b >= 0 && b+battMargin < best {
			best = b + battMargin
		}
	}
	return best
}

// --- tour simulation ---------------------------------------------------------

type simStart struct {
	pos     P
	t       int64
	batt    int
	maxBatt int
	class   engine.CarClassConfig
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// expand turns the work list into a runnable list by inserting a charge before
// any leg the battery cannot cover with its reserve, and returns each work stop's
// arrival tick. strict refuses a tour that needs more than one full battery for a
// leg; the runtime caller is not strict, because the order is already committed
// and the best it can do is charge as far as the battery allows.
func (pl *pilot) expand(st simStart, base []stop, strict bool) (out []stop, eta []int64, ok bool) {
	cur, t, batt := st.pos, st.t, st.batt
	eta = make([]int64, len(base))
	for i, s := range base {
		res := pl.reserveAt(st.class, s.pos)
		d := pl.dm(s.pos, st.class)
		steps, need := d.steps[pl.idx(cur)], d.batt[pl.idx(cur)]
		if steps < 0 {
			return nil, nil, false
		}
		if s.kind == skPickup && cur == s.pos {
			// Pickups fire on entering the tile, so a car already standing on the
			// kitchen steps off and back: two ticks and two steps of battery. The
			// zero-distance estimate left such a car stuck with a route its
			// reserve budget could not pay for.
			steps = max(steps, 2)
			need = max(need, 2*pl.w.City.WalkCost(s.pos, st.class))
		}
		if need+res > batt {
			bestTotal := -1
			var bestStop stop
			var bestSteps, bestTicks, bestHave, bestScore int
			bestCapped := false
			for _, ch := range pl.chargeTiles() {
				dc := pl.dm(ch, st.class)
				a, sa := dc.batt[pl.idx(cur)], dc.steps[pl.idx(cur)]
				if a < 0 || sa < 0 || a > batt {
					continue
				}
				need2, steps2 := d.batt[pl.idx(ch)], d.steps[pl.idx(ch)]
				if need2 < 0 || steps2 < 0 {
					continue
				}
				target := need2 + res
				// capped: even a full battery on this charger cannot cover the rest
				// (with its reserve), so the car would only be charging to full to
				// set off short. Such a charger is a fallback, never a first choice:
				// charging to full at a charger already 55 steps from the address
				// froze a car whose route the reserve budget could not pay for.
				capped := target > st.maxBatt
				if capped {
					if strict {
						continue
					}
					target = st.maxBatt
				}
				have := batt - a
				ticks := 0
				if target > have {
					ticks = ceilDiv(target-have, pl.rateAt(ch))
				} else {
					target = have
				}
				total := sa + ticks + steps2
				score := total
				if capped {
					// Among the unable, get as near the address as one battery allows.
					score = need2*1000 + total
				}
				if bestTotal < 0 || (bestCapped && !capped) ||
					(capped == bestCapped && (score < bestScore || (score == bestScore && pl.isDepot(ch)))) {
					bestTotal, bestScore, bestCapped = total, score, capped
					bestStop = stop{kind: skCharge, pos: ch, chargeTo: target}
					bestSteps, bestTicks, bestHave = sa, ticks, have
				}
			}
			if bestTotal < 0 {
				return nil, nil, false
			}
			out = append(out, bestStop)
			t += int64(bestSteps + bestTicks)
			batt = min(st.maxBatt, bestHave+bestTicks*pl.rateAt(bestStop.pos))
			cur = bestStop.pos
			steps, need = d.steps[pl.idx(cur)], d.batt[pl.idx(cur)]
		}
		t += int64(steps + legSlack)
		batt -= need
		cur = s.pos
		eta[i] = t
		s.reserve = res
		out = append(out, s)
	}
	return out, eta, true
}

// estimateFrom is the engine's own deadline quote for a job started from a car at
// `from`: assignment re-quotes the deadline this way and only ever extends it.
func (pl *pilot) estimateFrom(from, kitchen, customer P) int64 {
	cfg := pl.cfg.Orders
	slack := cfg.DeadlineSlack
	if by := cfg.DeadlineSlackByChapter; len(by) > 0 {
		ch := min(pl.w.Chapter(), len(by)-1)
		if ch >= 0 {
			slack = by[ch]
		}
	}
	total := 0
	for _, leg := range [2]int{pl.w.RouteDistance(from, kitchen), pl.w.RouteDistance(kitchen, customer)} {
		if leg >= 0 {
			total += leg
		} else {
			total += int(cfg.UnreachableLegTicks)
		}
	}
	if int64(total) < cfg.MinRouteTicks {
		total = int(cfg.MinRouteTicks)
	}
	est := float64(total) * slack
	if est < float64(cfg.MinDeadlineTicks) {
		est = float64(cfg.MinDeadlineTicks)
	}
	return int64(est + 0.5)
}

func (pl *pilot) deadlineAfterAssign(o *engine.Order, carPos P) int64 {
	k, kok := pl.kitchens[o.KitchenID]
	c, cok := pl.customers[o.CustomerID]
	dl := o.DeadlineTick
	if kok && cok {
		if fresh := pl.w.Tick + pl.estimateFrom(carPos, k, c); fresh > dl {
			dl = fresh
		}
	}
	return dl
}

func (pl *pilot) startOf(car *engine.Car) simStart {
	return simStart{pos: car.Pos, t: pl.w.Tick, batt: car.Battery, maxBatt: car.Class.MaxBattery, class: car.Class}
}

// tourOK simulates base from the car's present state and reports whether every
// dropoff lands by its deadline and the hold never overflows. extra supplies the
// deadline of an order whose assignment would extend it.
func (pl *pilot) tourOK(car *engine.Car, base []stop, extra map[string]int64) (score, newETA, slack int64, ok bool) {
	load := car.Load()
	for _, s := range base {
		o, found := pl.w.Order(s.order)
		if !found {
			return 0, 0, 0, false
		}
		switch s.kind {
		case skPickup:
			load += o.Boxes()
			if load > car.Class.Capacity {
				return 0, 0, 0, false
			}
		case skDropoff:
			load -= o.Boxes()
		}
	}
	_, eta, ok := pl.expand(pl.startOf(car), base, true)
	if !ok {
		return 0, 0, 0, false
	}
	slack = 1 << 40
	for i, s := range base {
		if s.kind != skDropoff {
			continue
		}
		o, _ := pl.w.Order(s.order)
		dl := o.DeadlineTick
		if v, has := extra[s.order]; has {
			dl = v
		}
		if eta[i] > dl {
			return 0, 0, 0, false
		}
		score += eta[i]
		slack = min(slack, dl-eta[i])
		if _, has := extra[s.order]; has {
			newETA = eta[i]
		}
	}
	return score, newETA, slack, true
}

// --- assignment --------------------------------------------------------------

func (pl *pilot) reservedBoxes(car *engine.Car) int {
	n := 0
	for _, o := range pl.w.Orders {
		if o.State == engine.OrderAssigned && o.AssignedCar == car.ID {
			n += o.Boxes()
		}
	}
	return n
}

// insertion finds the cheapest valid position for o's pickup and dropoff in d's
// work list.
func (pl *pilot) insertion(d *driver, o *engine.Order, deadline int64) (base []stop, score, eta, slack int64, ok bool) {
	k, kok := pl.kitchens[o.KitchenID]
	c, cok := pl.customers[o.CustomerID]
	// A kitchen or address closed for street works cannot be entered, so the
	// order cannot be served until the works end, far past its deadline.
	if !kok || !cok || !pl.w.RouteWalkable(k) || !pl.w.RouteWalkable(c) {
		return nil, 0, 0, 0, false
	}
	pk := stop{kind: skPickup, pos: k, order: o.ID}
	dr := stop{kind: skDropoff, pos: c, order: o.ID}
	extra := map[string]int64{o.ID: deadline}
	n := len(d.base)
	best := int64(-1)
	for i := 0; i <= n; i++ {
		for j := i; j <= n; j++ {
			cand := make([]stop, 0, n+2)
			cand = append(cand, d.base[:i]...)
			cand = append(cand, pk)
			cand = append(cand, d.base[i:j]...)
			cand = append(cand, dr)
			cand = append(cand, d.base[j:]...)
			sc, e, sl, good := pl.tourOK(d.car, cand, extra)
			if !good {
				continue
			}
			// Prefer the tour that leaves the least-slack order the most room (up
			// to slackCap, past which more room buys nothing), then the one that
			// finishes soonest.
			sl = min(sl, slackCap)
			if best < 0 || sl > slack || (sl == slack && sc < best) {
				best, base, score, eta, slack = sc, cand, sc, e, sl
			}
		}
	}
	return base, score, eta, slack, best >= 0
}

// assignPending gives every pending order to the car whose tour finishes it
// soonest, when some tour can meet its deadline.
func (pl *pilot) assignPending() {
	var pending []*engine.Order
	for _, o := range pl.w.OpenOrders() {
		if o.State == engine.OrderPending {
			pending = append(pending, o)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].DeadlineTick != pending[j].DeadlineTick {
			return pending[i].DeadlineTick < pending[j].DeadlineTick
		}
		return pending[i].ID < pending[j].ID
	})
	for _, o := range pending {
		pl.tryAssign(o)
	}
}

func (pl *pilot) tryAssign(o *engine.Order) bool {
	var bestD *driver
	var bestBase []stop
	var bestETA, bestScore, bestSlack int64 = -1, 0, 0
	for _, d := range pl.drivers {
		car := d.car
		if car.State(pl.w.City) == engine.CarStranded {
			continue
		}
		if car.FreeBoxes() < pl.reservedBoxes(car)+o.Boxes() {
			continue
		}
		dl := pl.deadlineAfterAssign(o, car.Pos)
		if s := pl.seen[o.ID]; s != nil && s.assigned {
			// A handed-back order keeps the deadline it has. The engine re-quotes
			// a deadline on every assignment and only ever extends it, so counting
			// that would let an order be handed back and taken again, each lap
			// buying more time, until it never expires. Reassignment must be
			// justified by a car that meets the deadline the order already has.
			dl = o.DeadlineTick
		}
		base, score, eta, slack, ok := pl.insertion(d, o, dl)
		if !ok {
			continue
		}
		if bestETA < 0 || slack > bestSlack || (slack == bestSlack && (eta < bestETA || (eta == bestETA && score < bestScore))) {
			bestD, bestBase, bestETA, bestScore, bestSlack = d, base, eta, score, slack
		}
	}
	if bestD == nil {
		return false
	}
	if err := pl.w.AssignOrder(o.ID, bestD.car.ID); err != nil {
		pl.logf("assign %s -> %s refused: %v", o.ID, bestD.car.ID, err)
		return false
	}
	bestD.base = bestBase
	bestD.route = nil
	bestD.stayUntil = 0
	// A charge decided for the old tour no longer fits the new one: the tour was
	// simulated from where the car is now, and a car driving 25 tiles west to
	// charge before a pickup 13 tiles east of it would promise 27 ticks and take
	// 60. A car already standing on its charger keeps it; the new tour decides.
	if c := bestD.commit; c != nil && bestD.car.Pos != c.pos {
		bestD.commit = nil
	}
	if s := pl.seen[o.ID]; s != nil {
		s.assigned = true
		s.assignTick, s.predicted, s.car = pl.w.Tick, bestETA, bestD.car.ID
	}
	pl.logf("assign %s (%d boxes, dl %d) -> %s eta %d", o.ID, o.Boxes(), o.DeadlineTick, bestD.car.ID, bestETA)
	return true
}

// --- purchasing --------------------------------------------------------------

// maybeBuy buys the cheapest car that fits an order no existing car can serve
// in time, when a fresh car from the depot could. One purchase per tick.
func (pl *pilot) maybeBuy() bool {
	if len(pl.w.Cars) >= pl.w.TargetActive()+fleetSlack+needSlack || len(pl.w.City.Depots) == 0 {
		return false
	}
	var pending []*engine.Order
	for _, o := range pl.w.OpenOrders() {
		if o.State == engine.OrderPending {
			pending = append(pending, o)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].DeadlineTick < pending[j].DeadlineTick })
	depot := pl.w.City.Depots[0]
	for _, o := range pending {
		// A fresh car starts at the depot with a full battery. Try the order on
		// one before paying for it: the purchase must be what makes it servable.
		for _, cc := range pl.w.CarClassCatalog() {
			if !cc.Unlocked || cc.Capacity < o.Boxes() || pl.w.Ledger.Money < cc.Price+cashReserve {
				continue
			}
			class := engine.CarClassConfig{Capacity: cc.Capacity, MaxBattery: cc.MaxBattery, StepCost: cc.StepCost}
			probe, err := engine.NewCar("probe", "probe", cc.Class, depot.Pos, map[string]engine.CarClassConfig{cc.Class: class})
			if err != nil {
				continue
			}
			if _, _, _, _, ok := pl.insertion(&driver{car: probe}, o, pl.deadlineAfterAssign(o, depot.Pos)); !ok {
				continue
			}
			if _, err := pl.w.BuyCar(cc.Class, depot.ID); err != nil {
				continue
			}
			pl.bought++
			pl.logf("buy %s ($%d) for %s (%d boxes), money now $%d", cc.Class, cc.Price, o.ID, o.Boxes(), pl.w.Ledger.Money)
			return true
		}
	}
	return pl.buySurplus(depot)
}

// buySurplus spends idle money on capacity. Cash piles up past anything the
// planner can use ($100k by the end of a run), and a car that is free when an
// order lands is the one thing that beats a tight deadline.
func (pl *pilot) buySurplus(depot engine.Depot) bool {
	if pl.w.Ledger.Money < surplusCash || len(pl.w.Cars) >= pl.w.TargetActive()+fleetSlack {
		return false
	}
	for _, cc := range pl.w.CarClassCatalog() {
		if cc.Class != surplusClass || !cc.Unlocked || pl.w.Ledger.Money < cc.Price+cashReserve {
			continue
		}
		if _, err := pl.w.BuyCar(cc.Class, depot.ID); err != nil {
			return false
		}
		pl.bought++
		pl.logf("buy %s ($%d) with spare cash, money now $%d", cc.Class, cc.Price, pl.w.Ledger.Money)
		return true
	}
	return false
}

// --- the tick ----------------------------------------------------------------

func (pl *pilot) sync() {
	for _, c := range pl.w.Cars {
		if pl.byCar[c.ID] == nil {
			d := &driver{car: c}
			pl.byCar[c.ID] = d
			pl.drivers = append(pl.drivers, d)
		}
	}
	// Kitchens and customers can be added by growth, so refresh every tick.
	pl.kitchens = map[string]P{}
	for _, k := range pl.w.City.Kitchens {
		pl.kitchens[k.ID] = k.Pos
	}
	pl.customers = map[string]P{}
	for _, cu := range pl.w.City.Customers {
		pl.customers[cu.ID] = cu.Pos
	}
}

// prune drops work whose order is no longer this car's to do.
func (pl *pilot) prune(d *driver) {
	kept := d.base[:0]
	for _, s := range d.base {
		o, ok := pl.w.Order(s.order)
		if !ok || o.State.Terminal() || o.AssignedCar != d.car.ID {
			continue
		}
		if s.kind == skPickup && o.State != engine.OrderAssigned {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) != len(d.base) {
		d.route = nil
		d.stayUntil = 0
	}
	d.base = kept
}

func (pl *pilot) step() {
	pl.dmCache = map[dmKey]*dmap{}
	pl.sync()
	pl.track()
	pl.maybeBuyCharger()

	for _, d := range pl.drivers {
		// A car that cannot reach any charger on what it has left is already
		// lost, and a car waiting forever for a charger it cannot reach blocks
		// the lane it sits in. The tow fee is small next to a stranded fleet.
		if d.car.State(pl.w.City) == engine.CarStranded ||
			(!pl.w.City.ChargingAt(d.car.Pos) && !pl.canReachCharger(d.car)) {
			at, bat := d.car.Pos, d.car.Battery
			if err := pl.w.Tow(d.car.ID); err == nil {
				pl.logf("tow %s from %v with battery %d (stranded %v, closures %d)", d.car.ID, at, bat,
					bat <= 0, len(pl.w.ActiveClosures()))
			}
			d.route, d.stayUntil, d.commit = nil, 0, nil
		}
		pl.prune(d)
	}

	pl.rebalance()
	pl.assignPending()
	for pl.maybeBuy() {
		pl.sync()
		pl.assignPending()
	}

	// Plan in priority order: the car with the nearest deadline first, so a
	// later planner has to work around it and not the other way round.
	order := append([]*driver(nil), pl.drivers...)
	sort.SliceStable(order, func(i, j int) bool { return pl.urgency(order[i]) < pl.urgency(order[j]) })
	settled := map[*driver]bool{}
	for _, d := range order {
		if len(d.route) > 0 || pl.charging(d) {
			settled[d] = true
		}
	}
	for _, d := range order {
		if len(d.route) == 0 && !pl.charging(d) {
			pl.plan(d, settled)
			settled[d] = true
		}
	}
	pl.resolveConflicts(order, settled)
	pl.unstick(order, settled)

	for _, d := range pl.drivers {
		if len(d.route) == 0 {
			continue
		}
		next := d.route[0]
		if !pl.w.RouteWalkable(next) && next != d.car.Pos {
			d.route = nil
			continue
		}
		if next == d.car.Pos {
			continue
		}
		for _, dir := range engine.AllDirections {
			if nb, ok := d.car.Pos.Neighbour(dir); ok && nb == next {
				_ = pl.w.QueueMove(d.car.ID, dir)
				break
			}
		}
	}
}

func (pl *pilot) urgency(d *driver) int64 {
	best := int64(1 << 40)
	for _, s := range d.base {
		if o, ok := pl.w.Order(s.order); ok && o.DeadlineTick < best {
			best = o.DeadlineTick
		}
	}
	return best
}

// after checks the planned moves against what the engine did.
func (pl *pilot) after(evs []engine.Event) {
	for _, e := range evs {
		if e.Type == engine.EventBlocked {
			pl.blocked++
		}
	}
	for _, d := range pl.drivers {
		if len(d.route) == 0 {
			continue
		}
		want := d.route[0]
		d.route = d.route[1:]
		if d.car.Pos != want {
			pl.replans++
			d.route = nil
		}
	}
}

// plan gives a car whose route ran out its next leg.
func (pl *pilot) plan(d *driver, settled map[*driver]bool) {
	car := d.car
	if c := d.commit; c != nil {
		reach := pl.dm(c.pos, car.Class).batt[pl.idx(car.Pos)]
		switch {
		case car.Pos == c.pos && (car.Battery >= c.chargeTo || car.Full()):
			d.commit = nil
		case car.Pos != c.pos && (reach < 0 || reach > car.Battery):
			// The charger it chose is out of reach on what is left (the route
			// cost more than the estimate): choose again.
			d.commit = nil
		}
	}
	var stops []stop
	if d.commit != nil {
		stops = []stop{*d.commit}
	} else {
		var ok bool
		stops, _, ok = pl.expand(pl.startOf(car), d.base, false)
		if !ok {
			// A leg no charger can serve: charge fully, then try again.
			stops = nil
			if len(d.base) > 0 {
				if ch, found := pl.nearestCharger(car); found {
					stops = []stop{{kind: skCharge, pos: ch, chargeTo: car.Class.MaxBattery}}
				}
			}
		}
		if len(stops) > 0 && stops[0].kind == skCharge {
			c := stops[0]
			d.commit = &c
		}
	}
	if len(stops) == 0 {
		stops = pl.idleStops(d)
		if len(stops) == 0 {
			d.stayUntil = 0
			return
		}
	}
	head := stops[0]
	// Work behind a closed tile waits; the work that can be done goes first. A
	// carried order used to sit behind a pickup at a kitchen under repair for
	// its whole deadline.
	if head.kind == skPickup || head.kind == skDropoff {
		for _, s := range stops {
			if (s.kind == skPickup || s.kind == skDropoff) && pl.w.RouteWalkable(s.pos) {
				head = s
				break
			}
		}
	}

	if head.kind == skCharge && car.Pos == head.pos {
		if car.Battery >= head.chargeTo || car.Full() {
			// Done charging, but expand said a charge is still due: the
			// target was rounded up. Move on to the work.
			if len(d.base) > 0 {
				head = pl.firstWork(stops)
			} else {
				return
			}
		} else {
			d.stayUntil = pl.w.Tick + int64(ceilDiv(head.chargeTo-car.Battery, pl.rateAt(head.pos)))
			return
		}
	}
	d.stayUntil = 0

	budget := car.Battery - head.reserve
	if head.kind == skCharge || head.kind == skPark {
		budget = car.Battery
	}
	mustMove := head.kind == skPickup || head.kind == skDropoff || car.Pos == head.pos
	route := pl.search(d, head.pos, budget, mustMove, settled)
	if route == nil {
		route = pl.evade(d, head.pos, settled)
		if route != nil {
			pl.logf("%s yields from %v", car.ID, car.Pos)
		}
	}
	d.route = route
}

// resolveConflicts replans any car whose route runs through a car that turned out
// to stay put. Planning is in priority order, so a car planned early cannot see
// that one planned later will not move; the stationary car is settled by now.
func (pl *pilot) resolveConflicts(order []*driver, settled map[*driver]bool) {
	for pass := 0; pass < 4; pass++ {
		var redo []*driver
		for _, x := range order {
			if len(x.route) > 0 && pl.hitsStationary(x) {
				redo = append(redo, x)
			}
		}
		if len(redo) == 0 {
			return
		}
		for _, x := range redo {
			x.route = nil
			delete(settled, x)
		}
		for _, x := range redo {
			pl.plan(x, settled)
			settled[x] = true
		}
	}
}

func (pl *pilot) hitsStationary(x *driver) bool {
	for _, s := range pl.drivers {
		if s == x || len(s.route) > 0 || pl.w.City.CarCapacity(s.car.Pos) > 1 {
			continue
		}
		for _, p := range x.route {
			if p == s.car.Pos {
				return true
			}
		}
	}
	return false
}

// idleStops is what a car with no work does: charge if it stands on a charger and
// is not full, go home if the battery reaches the depot, else go to the nearest
// charger it can reach. Parking on the depot, which holds the whole fleet, keeps
// one-lane streets clear.
func (pl *pilot) idleStops(d *driver) []stop {
	car := d.car
	if len(pl.w.City.Depots) == 0 {
		return nil
	}
	depot := pl.w.City.Depots[0].Pos
	if car.Pos == depot {
		return nil
	}
	b := pl.dm(depot, car.Class).batt[pl.idx(car.Pos)]
	canHome := b >= 0 && b+battMargin <= car.Battery
	if pl.w.City.ChargingAt(car.Pos) && !car.Full() {
		c := stop{kind: skCharge, pos: car.Pos, chargeTo: car.Class.MaxBattery}
		d.commit = &c
		return []stop{c}
	}
	if canHome {
		return []stop{{kind: skPark, pos: depot}}
	}
	if ch, ok := pl.hopToward(car, depot); ok {
		c := stop{kind: skCharge, pos: ch, chargeTo: car.Class.MaxBattery}
		d.commit = &c
		return []stop{c}
	}
	if ch, ok := pl.nearestCharger(car); ok && ch != car.Pos {
		c := stop{kind: skCharge, pos: ch, chargeTo: car.Class.MaxBattery}
		d.commit = &c
		return []stop{c}
	}
	return nil
}

// hopToward picks the charger the car can reach that is nearest the target and
// nearer than the car is, so a car too far from the depot to drive home on one
// battery works its way back charger by charger instead of parking on a
// one-car charger others need.
func (pl *pilot) hopToward(car *engine.Car, target P) (P, bool) {
	home := pl.dm(target, car.Class)
	here := home.batt[pl.idx(car.Pos)]
	if here < 0 {
		return P{}, false
	}
	best, bestB, found := P{}, 0, false
	for _, ch := range pl.chargeTiles() {
		if ch == car.Pos {
			continue
		}
		reach := pl.dm(ch, car.Class).batt[pl.idx(car.Pos)]
		left := home.batt[pl.idx(ch)]
		if reach < 0 || reach+battMargin > car.Battery || left < 0 || left >= here {
			continue
		}
		if !found || left < bestB {
			best, bestB, found = ch, left, true
		}
	}
	return best, found
}

func (pl *pilot) canReachCharger(car *engine.Car) bool {
	for _, ch := range pl.chargeTiles() {
		if b := pl.dm(ch, car.Class).batt[pl.idx(car.Pos)]; b >= 0 && b <= car.Battery {
			return true
		}
	}
	return false
}

// charging reports a car that is sitting on its committed charger, waiting for
// the battery: it will not move, so every other planner must treat its tile as
// taken whether or not it has been planned yet this tick.
func (pl *pilot) charging(d *driver) bool {
	c := d.commit
	if c == nil || d.car.Pos != c.pos || d.car.Battery >= c.chargeTo || d.car.Full() {
		return false
	}
	d.stayUntil = pl.w.Tick + int64(ceilDiv(c.chargeTo-d.car.Battery, pl.rateAt(c.pos)))
	return true
}

func (pl *pilot) firstWork(stops []stop) stop {
	for _, s := range stops {
		if s.kind == skPickup || s.kind == skDropoff {
			return s
		}
	}
	return stops[0]
}

func (pl *pilot) nearestCharger(car *engine.Car) (P, bool) {
	best, found := P{}, false
	bd := 0
	for _, ch := range pl.chargeTiles() {
		d := pl.dm(ch, car.Class)
		s, b := d.steps[pl.idx(car.Pos)], d.batt[pl.idx(car.Pos)]
		if s < 0 || b < 0 || b > car.Battery {
			continue
		}
		if !found || s < bd {
			best, bd, found = ch, s, true
		}
	}
	return best, found
}

// --- space-time search -------------------------------------------------------

type occKey struct{ tile, t int }

// posAt is where d is expected at tick offset k from now. ok is false once a
// charging car is expected to have left.
func (pl *pilot) posAt(d *driver, k int) (P, bool) {
	if k == 0 {
		return d.car.Pos, true
	}
	if k <= len(d.route) {
		return d.route[k-1], true
	}
	p := d.car.Pos
	if len(d.route) > 0 {
		p = d.route[len(d.route)-1]
		// A car that reaches the end of its route is on a pickup, a dropoff or
		// the way home, and drives on the next tick. Holding its tile forever
		// made every kitchen look permanently taken the moment one car was
		// about to collect there, and cars boxed in a dead-end pocket sat out
		// their orders. Only a car about to charge stays put.
		if d.commit == nil {
			return P{}, false
		}
	}
	if d.stayUntil > 0 && pl.w.Tick+int64(k) > d.stayUntil && !pl.isDepot(p) {
		return P{}, false
	}
	return p, true
}

func (pl *pilot) occupancy(skip *driver, settled map[*driver]bool) map[occKey][]*driver {
	occ := map[occKey][]*driver{}
	for _, d := range pl.drivers {
		if d == skip || !settled[d] {
			continue
		}
		for k := 0; k <= horizon+1; k++ {
			if p, ok := pl.posAt(d, k); ok {
				key := occKey{pl.idx(p), k}
				occ[key] = append(occ[key], d)
			}
		}
	}
	return occ
}

type stNode struct{ f, h, id int }
type stHeap []stNode

func (h stHeap) Len() int { return len(h) }
func (h stHeap) Less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	return h[i].h < h[j].h
}
func (h stHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *stHeap) Push(x any)   { *h = append(*h, x.(stNode)) }
func (h *stHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// search plans a route for d to goal, avoiding every settled car's committed
// positions. mustMove demands the car arrive by a move (pickups and dropoffs
// fire on entering the tile, not on standing there). It returns nil when no route
// fits inside the horizon and the battery budget.
func (pl *pilot) search(d *driver, goal P, budget int, mustMove bool, settled map[*driver]bool) []P {
	dmGoal := pl.dm(goal, d.car.Class)
	return pl.astar(d, budget, settled, func(p P, t int, byMove bool) bool {
		return p == goal && t >= 1 && (byMove || !mustMove)
	}, func(p P) int { return dmGoal.steps[pl.idx(p)] }, func(p P) int { return dmGoal.batt[pl.idx(p)] })
}

// evade finds the nearest tile nobody else is planned to use and goes there, so a
// car boxed in a one-lane street backs out instead of waiting on the car that is
// waiting on it.
func (pl *pilot) evade(d *driver, goal P, settled map[*driver]bool) []P {
	occ := pl.occupancy(d, settled)
	busy := map[int]bool{}
	for key := range occ {
		if key.t >= 1 {
			busy[key.tile] = true
		}
	}
	if !busy[pl.idx(d.car.Pos)] {
		return nil // nobody needs this tile: waiting is free
	}
	return pl.astar(d, max(d.car.Battery-evadeReserve, 0), settled, func(p P, t int, byMove bool) bool {
		return t >= 1 && byMove && !busy[pl.idx(p)] && p != goal
	}, func(P) int { return 0 }, func(P) int { return 0 })
}

func (pl *pilot) astar(d *driver, budget int, settled map[*driver]bool, isGoal func(P, int, bool) bool, h, hBatt func(P) int) []P {
	car := d.car
	cells := pl.n * pl.n
	occ := pl.occupancy(d, settled)
	total := cells * (horizon + 1)
	parent := make([]int32, total)
	used := make([]int16, total)
	seen := make([]bool, total)
	byMove := make([]bool, total)

	id := func(tile, t int) int { return t*cells + tile }
	start := pl.idx(car.Pos)
	seen[id(start, 0)] = true
	open := &stHeap{{h: max(h(car.Pos), 0), f: max(h(car.Pos), 0), id: id(start, 0)}}

	for open.Len() > 0 {
		cur := heap.Pop(open).(stNode)
		tile, t := cur.id%cells, cur.id/cells
		p := P{X: tile % pl.n, Y: tile / pl.n}
		if isGoal(p, t, byMove[cur.id]) {
			var rev []P
			for at := cur.id; at != id(start, 0); at = int(parent[at]) {
				tl := at % cells
				rev = append(rev, P{X: tl % pl.n, Y: tl / pl.n})
			}
			route := make([]P, len(rev))
			for i := range rev {
				route[len(rev)-1-i] = rev[i]
			}
			return route
		}
		if t >= horizon {
			continue
		}
		// Wait, then each direction.
		cands := []P{p}
		for _, dir := range engine.AllDirections {
			if nb, ok := p.Neighbour(dir); ok && pl.inGrid(nb) {
				cands = append(cands, nb)
			}
		}
		for ci, nb := range cands {
			move := ci > 0
			nt := t + 1
			ni := pl.idx(nb)
			if move && !pl.w.RouteWalkable(nb) {
				continue
			}
			if len(occ[occKey{ni, nt}]) >= pl.w.City.CarCapacity(nb) {
				continue
			}
			if move {
				// A swap with a car going the other way is refused by the engine.
				swap := false
				for _, o := range occ[occKey{ni, t}] {
					if q, ok := pl.posAt(o, nt); ok && q == p {
						swap = true
						break
					}
				}
				if swap {
					continue
				}
			}
			u := int(used[cur.id])
			if move {
				u += pl.w.City.WalkCost(nb, car.Class)
				if u > budget {
					continue
				}
				if hb := hBatt(nb); hb >= 0 && u+hb > budget {
					continue
				}
			}
			nid := id(ni, nt)
			if seen[nid] {
				// A move onto a tile first reached by waiting upgrades it, so a
				// car already standing on its goal can still be asked to re-enter.
				if move && !byMove[nid] && isGoal(nb, nt, true) {
					byMove[nid] = true
					parent[nid] = int32(cur.id)
					used[nid] = int16(u)
					heap.Push(open, stNode{f: nt, id: nid})
				}
				continue
			}
			seen[nid] = true
			parent[nid] = int32(cur.id)
			used[nid] = int16(u)
			byMove[nid] = move
			hv := h(nb)
			if hv < 0 {
				hv = 0
			}
			heap.Push(open, stNode{f: nt + hv, h: hv, id: nid})
		}
	}
	return nil
}

// --- reporting ---------------------------------------------------------------

// track records new orders and classifies expiries.
func (pl *pilot) track() {
	bestCap := 0
	for _, cc := range pl.w.CarClassCatalog() {
		if cc.Unlocked && cc.Capacity > bestCap {
			bestCap = cc.Capacity
		}
	}
	for _, o := range pl.w.Orders {
		s := pl.seen[o.ID]
		if s == nil {
			s = &spawnInfo{tick: pl.w.Tick, boxes: o.Boxes(), bestCap: bestCap}
			pl.seen[o.ID] = s
		}
		if o.State == engine.OrderExpired && !pl.counted[o.ID] {
			pl.expiries[pl.classify(o, s)]++
			if s.assigned {
				pl.logf("EXPIRED %s %d boxes: created %d, deadline %d, assigned at %d to %s promising %d",
					o.ID, o.Boxes(), o.CreatedTick, o.DeadlineTick, s.assignTick, s.car, s.predicted)
			} else {
				pl.logf("EXPIRED %s %d boxes: created %d, deadline %d, never assigned", o.ID, o.Boxes(), o.CreatedTick, o.DeadlineTick)
			}
			pl.counted[o.ID] = true
		}
	}
}

// unavoidable prefixes the causes no driver could have prevented.
const unavoidable = "unavoidable: "

func (pl *pilot) classify(o *engine.Order, s *spawnInfo) string {
	if s.assigned || o.AssignedCar != "" {
		return "assigned, then missed"
	}
	ownCap := 0
	for _, c := range pl.w.Cars {
		ownCap = max(ownCap, c.Class.Capacity)
	}
	if s.boxes <= ownCap {
		return "no tour met its deadline"
	}
	// Nothing in the fleet can carry it. It was only avoidable if a class that
	// fits could have been bought in time.
	cheapest := -1
	for _, cc := range pl.w.CarClassCatalog() {
		if cc.Unlocked && cc.Capacity >= s.boxes && (cheapest < 0 || cc.Price < cheapest) {
			cheapest = cc.Price
		}
	}
	if cheapest < 0 || pl.w.Ledger.Money < cheapest+cashReserve {
		return unavoidable + "no car could carry it and none was affordable"
	}
	return "affordable car bought too late"
}

func (pl *pilot) row() {
	w := pl.w
	stranded := 0
	for _, c := range w.Cars {
		if c.State(w.City) == engine.CarStranded {
			stranded++
		}
	}
	fmt.Printf("%6d %5d %5d %6d %5d %7d %7d %7d %6.2f %4d %7d\n",
		w.Tick, w.ActiveOrderCount(), w.TargetActive(), w.Ledger.Deliveries, w.Ledger.LateDeliveries,
		w.Ledger.ExpiredOrders, stranded, w.Ledger.Money, w.Ledger.Reputation, len(w.Cars), pl.blocked)
}

func (pl *pilot) summary() {
	w := pl.w
	l := w.Ledger
	fmt.Println("summary")
	fmt.Printf("  deliveries        %d (%d late)\n", l.Deliveries, l.LateDeliveries)
	fmt.Printf("  expired orders    %d (unclaimed %d, assigned %d, in transit %d)\n",
		l.ExpiredOrders, l.ExpiredUnclaimed, l.ExpiredAssigned, l.ExpiredInTransit)
	fmt.Printf("  orders spawned    %d\n", l.OrdersSpawned)
	fmt.Printf("  money earned      $%d (balance $%d)\n", l.MoneyEarned, l.Money)
	fmt.Printf("  reputation        %.2f\n", l.Reputation)
	fmt.Printf("  tows              %d\n", l.Tows)
	fmt.Printf("  blocked moves     %d (replans after a refused move: %d)\n", pl.blocked, pl.replans)
	fmt.Printf("  cars owned        %d %v\n", len(w.Cars), fleetBreakdown(w))
	if len(pl.expiries) > 0 {
		fmt.Println("  expiries by cause:")
		keys := make([]string, 0, len(pl.expiries))
		for k := range pl.expiries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("    %3d  %s\n", pl.expiries[k], k)
		}
		fmt.Printf("  avoidable expiries %d of %d\n", pl.avoidable(), l.ExpiredOrders)
	}
}

// maybeBuyCharger spends spare money on a charger where it shortens the trip to
// charge the most. A charger costs $150 and the balance ends in six figures; the
// far corners of the map (a customer 12 tiles from the nearest charger) are where
// cars run low, wait for the one slot, and miss deadlines. It places one at a time
// on the open road tile that cuts the most distance, summed over every kitchen
// and customer, to its nearest charging tile.
func (pl *pilot) maybeBuyCharger() {
	w := pl.w
	if w.Tick%chargerEvery != 0 || len(pl.drivers) == 0 {
		return
	}
	price, limit := w.City.Charging.ChargerPurchasePrice, w.City.Charging.MaxPurchasedChargers
	if limit <= 0 || len(w.PlacedChargers) >= limit || w.Ledger.Money < price+chargerCash {
		return
	}
	class := pl.drivers[0].car.Class
	var sites []P
	for _, k := range w.City.Kitchens {
		sites = append(sites, k.Pos)
	}
	for _, c := range w.City.Customers {
		sites = append(sites, c.Pos)
	}
	cur := make([]int, len(sites))
	for i, s := range sites {
		cur[i] = -1
		for _, ch := range pl.chargeTiles() {
			if d := pl.dm(ch, class).steps[pl.idx(s)]; d >= 0 && (cur[i] < 0 || d < cur[i]) {
				cur[i] = d
			}
		}
	}
	taken := map[P]bool{}
	for _, s := range sites {
		taken[s] = true
	}
	for _, ch := range pl.chargeTiles() {
		taken[ch] = true
	}
	best, bestGain := P{}, 0
	for y := range pl.n {
		for x := range pl.n {
			t := P{X: x, Y: y}
			cell, ok := w.City.At(t)
			if !ok || taken[t] || (cell != engine.RoadTile && cell != engine.GroundTile) ||
				!w.RouteWalkable(t) || w.CarsAt(t) > 0 {
				continue
			}
			m := pl.dm(t, class)
			gain := 0
			for i, s := range sites {
				if d := m.steps[pl.idx(s)]; d >= 0 && cur[i] >= 0 && d < cur[i] {
					// Squared, so a site far from every charger outweighs several
					// near ones: the far corner is where cars run dry.
					gain += cur[i]*cur[i] - d*d
				}
			}
			if gain > bestGain {
				best, bestGain = t, gain
			}
		}
	}
	if bestGain < chargerMinGain {
		return
	}
	if _, err := w.BuyCharger(best); err != nil {
		return
	}
	pl.logf("buy charger at %v (cuts %d tiles of site-to-charger distance), money now $%d", best, bestGain, w.Ledger.Money)
}

// avoidable is the number of expiries that are not unavoidable.
func (pl *pilot) avoidable() int {
	n := 0
	for cause, k := range pl.expiries {
		if !strings.HasPrefix(cause, unavoidable) {
			n += k
		}
	}
	return n
}

// rebalance takes an order back from a car that, on its present state, will reach
// the customer after the deadline, so the next assignment pass can give it to a
// car that will not. Only orders still waiting to be collected can move; a
// carried order is that car's to deliver. Traffic, a longer charge than planned
// or an order inserted ahead of it can all turn a tour that was on time at
// assignment into a late one.
func (pl *pilot) rebalance() {
	if !pl.handBack {
		return
	}
	for _, d := range pl.drivers {
		if len(d.base) == 0 {
			continue
		}
		_, eta, ok := pl.expand(pl.startOf(d.car), d.base, false)
		if !ok {
			continue
		}
		var late []string
		for i, s := range d.base {
			if s.kind != skDropoff {
				continue
			}
			o, found := pl.w.Order(s.order)
			if found && o.State == engine.OrderAssigned && eta[i]+handBackMargin > o.DeadlineTick {
				late = append(late, o.ID)
			}
		}
		for _, id := range late {
			if err := pl.w.UnassignOrder(id); err != nil {
				continue
			}
			kept := d.base[:0]
			for _, s := range d.base {
				if s.order != id {
					kept = append(kept, s)
				}
			}
			d.base = kept
			d.route, d.stayUntil = nil, 0
			pl.logf("%s hands back %s (would arrive late)", d.car.ID, id)
		}
	}
}

// unstick breaks a standoff that priority order cannot: a car with work that found
// no route because a car standing in its way was not planned yet when it looked,
// and that car then found no route either because the first one had already
// reserved the way out. In a kitchen with a single access tile the car inside has
// to leave before the one outside can enter, whichever has the nearer deadline.
// The blocker plans first, then the stuck car, against the blocker's new route.
func (pl *pilot) unstick(order []*driver, settled map[*driver]bool) {
	for _, x := range order {
		if len(x.route) > 0 || len(x.base) == 0 || pl.charging(x) {
			continue
		}
		for _, s := range order {
			if s == x || len(s.route) > 0 || pl.charging(s) || pl.w.City.CarCapacity(s.car.Pos) > 1 {
				continue
			}
			delete(settled, x)
			delete(settled, s)
			pl.plan(s, settled)
			settled[s] = true
			pl.plan(x, settled)
			settled[x] = true
			if len(x.route) > 0 {
				pl.logf("%s steps clear so %s can reach its stop", s.car.ID, x.car.ID)
				break
			}
		}
	}
}

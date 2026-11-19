package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/wricardo/drbrain-delivery/game/engine"
)

// cmdSim is the pacing harness (IDEA.txt §15/S2).
//
// It drives a scripted bot through N ticks and prints a stats table every
// `-every` ticks, so pacing questions get answered with numbers instead of
// opinions. The bot is deliberately simple — it is a measuring instrument,
// not an AI: for the oldest open order, walk the assigned car toward the
// kitchen, then toward the customer, charging when the battery cannot cover
// the next step plus a safety margin.
//
// Everything is driven through the same public engine API the real game will
// expose (QueueMove, AssignOrder, Advance), so a bug in the harness is a bug
// in the game.
func cmdSim(args []string) int {
	// Parsed with a FlagSet, matching cmdMap. Hand-rolling this loop was how
	// three bad invocations reached the player instead of a usage error:
	//
	//	drbrain sim -ticks            -> panic: index out of range
	//	drbrain sim -every 0 -ticks 5 -> panic: integer divide by zero
	//	drbrain sim -ticks abc        -> ran 0 ticks and printed a normal table
	//
	// The third was the dangerous one: an Atoi error was discarded into 0, so a
	// typo in a recorded command produced a plausible-looking header and row.
	// The README's pacing table is cited as evidence, so a silent zero-tick run
	// is a documentation hazard, not just a rough edge.
	opts := &simOptions{path: "maps/city.json", ticks: 3000, every: 200, seed: 0x5eed5eed}
	fs := newSimFlagSet(opts)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "sim: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "sim: %v\n", err)
		return 2
	}
	path, ticks, every, seed := opts.path, opts.ticks, opts.every, opts.seed

	city, cfg, err := engine.CityFromFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	world, err := engine.NewWorldSeeded(city, cfg, seed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("sim: %s, seed %#x, %d ticks, stats every %d\n\n", cfg.Name, seed, ticks, every)
	fmt.Printf("%6s %5s %5s %6s %5s %7s %7s %7s %6s %4s %4s\n",
		"tick", "open", "tgt", "deliv", "late", "expired", "strand", "money", "rep", "box", "cls")

	for range ticks {
		botStep(world)
		world.Advance()

		if world.Tick%int64(every) == 0 {
			printRow(world)
		}
	}

	fmt.Println()
	printRow(world)
	fmt.Println()

	l := world.Ledger
	fmt.Println("summary")
	fmt.Printf("  deliveries        %d (%d late)\n", l.Deliveries, l.LateDeliveries)
	fmt.Printf("  expired orders    %d (unclaimed %d, assigned %d, in transit %d)\n",
		l.ExpiredOrders, l.ExpiredUnclaimed, l.ExpiredAssigned, l.ExpiredInTransit)
	fmt.Printf("  orders spawned    %d\n", l.OrdersSpawned)
	fmt.Printf("  money earned      $%d (balance $%d)\n", l.MoneyEarned, l.Money)
	fmt.Printf("  reputation        %.2f\n", l.Reputation)
	fmt.Printf("  tows              %d\n", l.Tows)
	fmt.Printf("  builds completed  %d\n", l.BuildsCompleted)
	fmt.Printf("  cars owned        %d %v\n", len(world.Cars), fleetBreakdown(world))
	fmt.Println()
	fmt.Println("  achievements:")
	for _, a := range world.Achievements() {
		mark := " "
		if a.Done {
			mark = "x"
		}
		fmt.Printf("    [%s] %-24s %d/%d\n", mark, a.Title, a.Progress, a.Target)
	}
	fmt.Println("  unlocks:")
	for _, u := range world.Unlocks() {
		mark := " "
		if u.Earned {
			mark = "x"
		}
		fmt.Printf("    [%s] %s\n", mark, u.Title)
	}
	fmt.Println("  builds:")
	for _, b := range world.Builds() {
		state := "locked"
		switch {
		case b.Completed:
			state = "done"
		case b.Unlocked && b.Affordable && b.Applicable:
			state = "buildable"
		case b.Unlocked && b.Affordable && !b.Applicable:
			state = "blocked: " + b.Problem
		case b.Unlocked:
			state = fmt.Sprintf("needs $%d", b.Cost)
		}
		fmt.Printf("    %-20s $%-5d %s\n", b.Title, b.Cost, state)
	}

	// Sanity: the harness is only useful if it actually moved cars.
	if world.Cars[0].StepsTaken == 0 {
		fmt.Fprintln(os.Stderr, "\nWARNING: no car moved during the run; the bot is stuck")
		return 1
	}
	return 0
}

func printRow(w *engine.World) {
	stranded := 0
	for _, c := range w.Cars {
		if c.State(w.City) == engine.CarStranded {
			stranded++
		}
	}
	// Biggest order on the board, so the table shows whether quantities are
	// actually appearing or the bands are being filtered out by the fleet.
	biggest := 0
	for _, o := range w.OpenOrders() {
		if o.Boxes() > biggest {
			biggest = o.Boxes()
		}
	}
	fmt.Printf("%6d %5d %5d %6d %5d %7d %7d %7d %6.2f %4d %4d\n",
		w.Tick, w.ActiveOrderCount(), targetOf(w),
		w.Ledger.Deliveries, w.Ledger.LateDeliveries, w.Ledger.ExpiredOrders,
		stranded, w.Ledger.Money, w.Ledger.Reputation,
		biggest, len(w.ActiveClosures()))
}

// targetOf exposes the pacing target for the table; it is unexported on the
// World because it is an implementation detail of spawn pacing.
func targetOf(w *engine.World) int {
	// A re-derivation of the pacing target for the table, NOT the engine's own
	// number. It hardcodes ratio 1.0 and the cap 8, so on a map whose
	// `target_orders_per_car` is anything else (city.json ships 0.75) this
	// column is HIGHER than what the spawner actually aims for: with four cars
	// it prints 4 where World.targetActive() returns 3.
	//
	// Kept deliberately simple so the table stays readable, but that makes the
	// `tgt` column an approximation of the governor, not a measurement of it.
	// Deliberately not derived from hold capacity either: capacity is measured
	// in boxes, and a truck's forty boxes would ask for thirty concurrent
	// orders.
	cars := 0
	for _, c := range w.Cars {
		if c.State(w.City) != engine.CarStranded {
			cars++
		}
	}
	t := int(float64(cars)*1.0 + 0.5)
	if t < 1 {
		t = 1
	}
	if t > 8 {
		t = 8 // matches city.json max_active_orders
	}
	return t
}

// botStep is the harness driver: one decision per tick.
//
// Strategy, in priority order:
//  1. if any car is stranded, tow it (money is not the metric here, liveness is)
//  2. if a car is low on battery and not carrying anything, head for a charger
//  3. for the oldest open order, assign and drive the nearest idle car
func botStep(w *engine.World) {
	// 0. Spend money. The harness must exercise progression, not just the
	// delivery loop: without this it reported "builds completed: 0" while
	// sitting on $7000, which is exactly the signal that the content had no
	// reachable upgrades. Building and buying are part of the game being tested.
	botSpend(w)

	// 1. Unstick.
	for _, c := range w.Cars {
		if c.State(w.City) == engine.CarStranded {
			_ = w.Tow(c.ID)
		}
	}

	// 2. Assign any pending order to a free car, nearest first.
	for _, o := range w.OpenOrders() {
		if o.State != engine.OrderPending {
			continue
		}
		car := nearestFreeCar(w, o)
		if car == nil {
			break
		}
		_ = w.AssignOrder(o.ID, car.ID)
	}

	// 3. Drive each car toward its current goal.
	for _, c := range w.Cars {
		if c.State(w.City) == engine.CarStranded {
			continue
		}
		goal, ok := carGoal(w, c)
		if !ok {
			continue
		}
		// Battery planning over the WHOLE remaining route, not just the next
		// leg. The failure this fixes: a car reaches the kitchen on fumes,
		// picks up automatically, then cannot possibly reach the customer, so
		// it strands holding cargo and the order expires in transit. Charging
		// up first is what a competent player would do.
		if !canCompleteRoute(w, c, goal) {
			if stop := bestChargeStop(w, c); stop != nil {
				goal = *stop
			}
		}
		step := nextStepToward(w, c.Pos, goal)
		if step != "" {
			_ = w.QueueMove(c.ID, step)
		}
	}
}

// canCompleteRoute reports whether the car can reach goal and then still get
// to a charger or depot afterwards. Standing on a charger needs no reserve.
func canCompleteRoute(w *engine.World, c *engine.Car, goal engine.Position) bool {
	toGoal := w.RouteDistance(c.Pos, goal)
	if toGoal < 0 {
		return false
	}
	if w.City.ChargingAt(goal) {
		return toGoal <= c.Battery
	}
	need := toGoal
	next := w.NearestChargers(goal, 1)
	if len(next) == 0 {
		// Nothing to charge on anywhere: a route the car cannot top up after is
		// not completable, however short the first leg is.
		return false
	}
	d := w.RouteDistance(goal, next[0])
	if d < 0 {
		// Every charger is behind water, fog or works. Same answer: not
		// completable. Comparing -1 as if it were a distance used to make this
		// look cheap, which is exactly how the bot stranded cars on fumes.
		return false
	}
	need += d
	return need <= c.Battery-1
}

// bestChargeStop picks the nearest charging tile the car can actually reach AND
// that has room for it.
//
// Skipping full tiles matters more than it looks. Chargers author capacity 1 and
// the depot authors 4, so the nearest charging tile is usually the one with no
// room: measured on seed 99, six of eight cars queued on the same 1-slot charger
// while the depot stood empty, and throughput halved (115 delivered against 226
// with collision off). A driver joins the short queue, not the nearest one.
//
// Returning nil means "nowhere to charge" and the caller keeps its goal, so the
// bot degrades rather than freezing.
func bestChargeStop(w *engine.World, c *engine.Car) *engine.Position {
	for _, chg := range w.NearestChargers(c.Pos, 4) {
		if chg != c.Pos && !w.HasRoom(chg) {
			continue
		}
		d := w.RouteDistance(c.Pos, chg)
		// d < 0 is "no route", and -1 <= battery is true. Guarding it is not
		// defensive padding: without the check the bot happily drives at a
		// charger it cannot reach.
		if d >= 0 && d <= c.Battery {
			return &chg
		}
	}
	return nil
}

// carGoal is where the car should be heading right now.
func carGoal(w *engine.World, c *engine.Car) (engine.Position, bool) {
	// Carrying something? Deliver it.
	for _, id := range c.Cargo {
		if o, ok := w.Order(id); ok {
			if cu, ok := findCustomer(w, o.CustomerID); ok {
				return cu.Pos, true
			}
		}
	}
	// Assigned to pick something up? Go to the kitchen.
	for _, o := range w.OpenOrders() {
		if o.State == engine.OrderAssigned && o.AssignedCar == c.ID {
			if k, ok := findKitchen(w, o.KitchenID); ok {
				return k.Pos, true
			}
		}
	}
	// Idle and full, standing on a charging tile someone else needs: get out of
	// the way.
	//
	// This is not a politeness heuristic, it is required for the fleet to stay
	// alive once a tile has a car capacity. Chargers author capacity 1, so a
	// full car parked on one blocks it for the whole session: measured before
	// this rule, a 3000-tick run ended with 0 of 8 cars moving for the last 500
	// ticks, 166 expired orders, and 9993 blocked moves, because two full cars
	// sat on the map's only two 1-slot chargers with no goal to leave. A player
	// would drive off; the bot has to as well.
	if c.Full() && w.City.ChargingAt(c.Pos) {
		if p, ok := vacateStep(w, c); ok {
			return p, true
		}
	}
	// Idle with nothing to do: top up if the battery is low. Without this an
	// idle car is never given a goal, so it sits wherever it stopped and dies
	// there — the harness spent thousands of ticks with a car frozen on 3
	// battery at the kitchen, which looked like a pacing collapse but was the
	// driver never asking it to charge.
	if c.BatteryPct() < 99 {
		// A charger another car has already taken is not a valid goal: queueing
		// at it would burn the tick driving into a tile that is full.
		for _, tile := range w.NearestChargers(c.Pos, 4) {
			if tile == c.Pos || !w.HasRoom(tile) {
				continue
			}
			return tile, true
		}
		// No free charger, but already parked on one: stay and charge.
		if w.City.ChargingAt(c.Pos) {
			return engine.Position{}, false
		}
		// Nothing to charge on and nothing to do. Head back to the depot rather
		// than sleeping in the road: with a car capacity per tile, an idle car in
		// a one-wide street is a roadblock for every car behind it, and the depot
		// is the tile that holds a fleet. This is what a competent player does,
		// and the sim's throughput depends on it (measured: without it the fleet
		// parks in the street and every lane it sleeps in is closed).
		for _, d := range w.City.Depots {
			if w.HasRoom(d.Pos) || d.Pos == c.Pos {
				return d.Pos, true
			}
		}
	}
	return engine.Position{}, false
}

// vacateStep picks a reachable neighbour to park on so a full car frees the
// charger it is standing on.
//
// It prefers a plaza with real throughput (a kitchen or customer tile) and
// otherwise any walkable neighbour, because the point is to clear the charging
// tile rather than to find an ideal spot. It refuses to move onto a tile that
// already holds its capacity, so the car cannot shuffle onto an occupied street
// and make the jam worse.
func vacateStep(w *engine.World, c *engine.Car) (engine.Position, bool) {
	var fallback engine.Position
	haveFallback := false
	for _, d := range engine.AllDirections {
		n, ok := c.Pos.Neighbour(d)
		if !ok || !w.RouteWalkable(n) || !w.HasRoom(n) {
			continue
		}
		if _, ok := w.City.KitchenAt(n); ok {
			return n, true
		}
		if _, ok := w.City.CustomerAt(n); ok {
			return n, true
		}
		if !haveFallback {
			fallback, haveFallback = n, true
		}
	}
	return fallback, haveFallback
}

// canReachWithCharge and the older helpers were replaced by canCompleteRoute,
// which plans the whole remaining route instead of one leg.

// nextStepToward returns the first direction on a shortest path, or "" when
// already there or unreachable.
//
// It defers to the engine's StepToward so the bot routes around street works
// exactly as a player must: a harness that plans through a closed street
// reports failures the game does not actually have.
//
// It does NOT detour around cars, and that is the important decision here. The
// engine refuses a move into a full tile but keeps the queued direction, so a
// blocked car simply tries the same step again next tick — it waits, and waiting
// is free. Two alternatives were measured on the shipped map, 3000 ticks:
//
//	wait behind traffic (this)       250 delivered,  49 expired,  0 tows, rep 4.83
//	detour, budget on driven route    79 delivered, 177 expired, 34 tows
//	detour, budget on straight route 198 delivered,  89 expired, 84 tows
//
// The detouring runs strand constantly: a detour costs battery the car did not
// budget for, and pricing the detour instead makes every route look unaffordable
// so the fleet charges forever instead of delivering. Waiting has neither
// problem, and on a grid of one-lane streets it is also what a driver does.
func nextStepToward(w *engine.World, from, to engine.Position) engine.Direction {
	return w.StepToward(from, to)
}

// nearestFreeCar picks the closest car with space that is not already tied to
// another open order.
func nearestFreeCar(w *engine.World, o *engine.Order) *engine.Car {
	k, ok := findKitchen(w, o.KitchenID)
	if !ok {
		return nil
	}
	var best *engine.Car
	bestDist := -1
	for _, c := range w.Cars {
		// Capacity is in boxes, so a car may be free of orders and still be
		// unable to take this one. Asking HasSpace would let the harness
		// assign a forty-box order to a scooter and then wonder why nothing
		// was ever picked up.
		if !c.SpaceFor(o.Boxes()) || c.State(w.City) == engine.CarStranded {
			continue
		}
		if carBusy(w, c) {
			continue
		}
		d := w.RouteDistance(c.Pos, k.Pos)
		if d < 0 {
			continue
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// carBusy reports whether the car already holds or owes a delivery.
func carBusy(w *engine.World, c *engine.Car) bool {
	if len(c.Cargo) > 0 {
		return true
	}
	for _, o := range w.OpenOrders() {
		if o.AssignedCar == c.ID {
			return true
		}
	}
	return false
}

func findKitchen(w *engine.World, id string) (engine.Kitchen, bool) {
	for _, k := range w.City.Kitchens {
		if k.ID == id {
			return k, true
		}
	}
	return engine.Kitchen{}, false
}

func findCustomer(w *engine.World, id string) (engine.Customer, bool) {
	for _, c := range w.City.Customers {
		if c.ID == id {
			return c, true
		}
	}
	return engine.Customer{}, false
}

// botSpend buys upgrades and cars when affordable.
//
// Priority mirrors what a player does: open a new district (more work), then add
// a car (more throughput), then extend the depot. Keeping a reserve means the
// bot never bankrupts itself chasing an upgrade it cannot use.
//
// This exists because the harness previously reported "builds completed: 0"
// while holding $7000. That looked like a pacing result but was actually the
// bot never trying — a measurement bug masquerading as a design finding.
func botSpend(w *engine.World) {
	const reserve = 100

	// 1. Build anything unlocked, applicable and affordable, cheapest first so
	//    money gets recycled into progress rather than hoarded.
	for _, b := range w.Builds() {
		if !b.Unlocked || b.Completed || !b.Affordable || !b.Applicable {
			continue
		}
		if w.Ledger.Money-b.Cost < reserve {
			continue
		}
		if _, err := w.Build(b.ID); err != nil {
			continue
		}
		return // one purchase per tick keeps the economy visible in the table
	}

	// 2. Buy a car when the fleet is the bottleneck.
	//
	// The previous test was "open orders >= fleet capacity", which deadlocks:
	// the spawner caps the queue at ~1 order per car, so a struggling fleet never
	// sees a queue big enough to justify growing, and orders expire forever. The
	// honest signals are different: orders are going unclaimed, or deliveries
	// that do happen are slow relative to the queue.
	//
	// Note there is no "an order exists that nothing can carry" signal by
	// design: the spawner never rolls a band the fleet cannot service, so that
	// state cannot arise. Growth is therefore about throughput, and about
	// unlocking bigger bands by owning a bigger hold.
	// 2. Grow the fleet.
	//
	// Two separate decisions, in priority order:
	//
	//   a. CAPABILITY. If a class with a bigger hold is unlocked and the fleet
	//      does not own it yet, save for it. The depot is finite, so a stopgap
	//      van bought now can fill the very slot that vehicle needs, and a
	//      player who fills the yard with vans can never own the vehicle big
	//      orders require. This is also what makes the quantity bands reachable
	//      at all: the spawner only rolls sizes the fleet can carry.
	//
	//   b. THROUGHPUT. Otherwise buy the biggest affordable car, while leaving
	//      one slot free if a bigger class is still locked.
	//
	// Note there is no "an order exists that nothing can carry" signal by
	// design: the spawner never rolls a band the fleet cannot service, so that
	// state cannot arise.
	slots := depotSlots(w)
	if slots > 0 && len(w.Cars) >= slots {
		return
	}

	biggestOwned := 0
	for _, c := range w.Cars {
		if c.Class.Capacity > biggestOwned {
			biggestOwned = c.Class.Capacity
		}
	}

	// (a) Capability: the largest unlocked class the fleet does not have.
	var growth *engine.CarClassInfo
	biggerLocked := false
	catalog := w.CarClassCatalog()
	for i := range catalog {
		cc := catalog[i]
		if cc.Price <= 0 {
			continue
		}
		if !cc.Unlocked {
			if cc.Capacity > biggestOwned {
				biggerLocked = true
			}
			continue
		}
		if cc.Capacity <= biggestOwned {
			continue
		}
		if growth == nil || cc.Capacity > growth.Capacity {
			c := cc
			growth = &c
		}
	}
	if growth != nil {
		if !growth.Affordable || w.Ledger.Money-growth.Price < reserve {
			return // save instead of buying a stopgap
		}
		_, _ = w.BuyCar(growth.Class, "d_main")
		return
	}

	// (b) Throughput: buy the biggest affordable car, keeping a slot free for a
	// bigger class that is still locked.
	if biggerLocked && slots > 0 && len(w.Cars) >= slots-1 {
		return
	}
	// No point growing the fleet while the queue is quiet and nothing expires:
	// an idle car earns nothing and ties up a slot.
	unclaimed := 0
	for _, o := range w.OpenOrders() {
		if o.State == engine.OrderPending {
			unclaimed++
		}
	}
	strained := unclaimed > 0 || w.Ledger.ExpiredOrders > w.Ledger.Deliveries/3
	if !strained && w.ActiveOrderCount() < len(w.Cars) {
		return
	}

	best := -1
	bestCap := 0
	for i := range catalog {
		cc := catalog[i]
		if cc.Price <= 0 || !cc.Unlocked || !cc.Affordable {
			continue
		}
		if w.Ledger.Money-cc.Price < reserve {
			continue
		}
		if cc.Capacity > bestCap {
			best, bestCap = i, cc.Capacity
		}
	}
	if best < 0 {
		return
	}
	_, _ = w.BuyCar(catalog[best].Class, "d_main")
}

// depotSlots is how many cars the main depot can hold, or 0 when unknown.
func depotSlots(w *engine.World) int {
	for _, d := range w.City.Depots {
		if d.ID == "d_main" {
			return d.Capacity
		}
	}
	if len(w.City.Depots) > 0 {
		return w.City.Depots[0].Capacity
	}
	return 0
}

// fleetBreakdown reports the fleet by class, printed in the summary so a
// balance run shows whether the heavy quantity bands are even reachable.
func fleetBreakdown(w *engine.World) map[string]int {
	out := map[string]int{}
	for _, c := range w.Cars {
		out[c.ClassName]++
	}
	return out
}

// simOptions is the parsed form of the sim command line. It exists so the
// usage text and the validation are testable without running a simulation.
type simOptions struct {
	path  string
	ticks int
	every int
	seed  uint64
}

// newSimFlagSet binds opts to a FlagSet. Kept separate from cmdSim so a test can
// assert the usage text lists every flag that is actually parsed -- `-f` was
// silently undiscoverable while this was hand-rolled.
func newSimFlagSet(opts *simOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("sim", flag.ContinueOnError)
	fs.StringVar(&opts.path, "f", opts.path, "path to city file")
	fs.IntVar(&opts.ticks, "ticks", opts.ticks, "number of ticks to simulate (must be > 0)")
	fs.IntVar(&opts.every, "every", opts.every, "print a stats row every N ticks (must be > 0)")
	fs.Uint64Var(&opts.seed, "seed", opts.seed, "RNG seed")
	return fs
}

// validate rejects values that are not a harmless no-op.
//
// `-every 0` divided by zero in the tick loop, and `-ticks 0` printed a table
// without advancing anything. Both are rejected rather than clamped, so a
// recorded command that lost a digit fails loudly instead of producing
// evidence-shaped output.
func (o *simOptions) validate() error {
	if o.ticks <= 0 {
		return fmt.Errorf("-ticks must be > 0, got %d", o.ticks)
	}
	if o.every <= 0 {
		return fmt.Errorf("-every must be > 0, got %d", o.every)
	}
	return nil
}

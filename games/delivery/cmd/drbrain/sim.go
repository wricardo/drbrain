package main

import (
	"fmt"
	"os"
	"strconv"

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
	path := "maps/city.json"
	ticks := 3000
	every := 200
	seed := uint64(0x5eed5eed)

	// Tiny hand-rolled flag parsing: the CLI is small enough that pulling in a
	// flag set per command adds more noise than it removes.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f":
			i++
			path = args[i]
		case "-ticks":
			i++
			ticks, _ = strconv.Atoi(args[i])
		case "-every":
			i++
			every, _ = strconv.Atoi(args[i])
		case "-seed":
			i++
			v, _ := strconv.ParseUint(args[i], 0, 64)
			seed = v
		default:
			fmt.Fprintf(os.Stderr, "unknown sim flag %q\n", args[i])
			return 2
		}
	}

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
	fmt.Printf("%6s %5s %5s %6s %5s %7s %7s %7s %6s\n",
		"tick", "open", "tgt", "deliv", "late", "expired", "strand", "money", "rep")

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
	fmt.Printf("  cars owned        %d\n", len(world.Cars))
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
	fmt.Printf("%6d %5d %5d %6d %5d %7d %7d %7d %6.2f\n",
		w.Tick, w.ActiveOrderCount(), targetOf(w),
		w.Ledger.Deliveries, w.Ledger.LateDeliveries, w.Ledger.ExpiredOrders,
		stranded, w.Ledger.Money, w.Ledger.Reputation)
}

// targetOf exposes the pacing target for the table; it is unexported on the
// World because it is an implementation detail of spawn pacing.
func targetOf(w *engine.World) int {
	// Recomputed here from the same rule so the harness does not need engine
	// internals: target = round(capacity * ratio), clamped to [1, max].
	cap := 0
	for _, c := range w.Cars {
		if c.State(w.City) != engine.CarStranded {
			cap += c.Class.Capacity
		}
	}
	t := int(float64(cap)*0.75 + 0.5)
	if t < 1 {
		t = 1
	}
	if m := w.City.GridSize; m > 0 && t > 6 {
		t = 6 // matches city.json max_active_orders
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
	toGoal := w.City.Distance(c.Pos, goal)
	if toGoal < 0 {
		return false
	}
	if w.City.ChargingAt(goal) {
		return toGoal <= c.Battery
	}
	need := toGoal
	if next := w.City.NearestChargingTiles(goal, 1); len(next) > 0 {
		if d := w.City.Distance(goal, next[0]); d > 0 {
			need += d
		}
	}
	return need <= c.Battery-1
}

// bestChargeStop picks the nearest charging tile the car can actually reach.
// Returning nil means "nowhere to charge" and the caller keeps its goal, so
// the bot degrades rather than freezing.
func bestChargeStop(w *engine.World, c *engine.Car) *engine.Position {
	for _, chg := range w.City.NearestChargingTiles(c.Pos, 4) {
		if w.City.Distance(c.Pos, chg) <= c.Battery {
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
	// Idle with nothing to do: top up if the battery is low. Without this an
	// idle car is never given a goal, so it sits wherever it stopped and dies
	// there — the harness spent thousands of ticks with a car frozen on 3
	// battery at the kitchen, which looked like a pacing collapse but was the
	// driver never asking it to charge.
	if c.BatteryPct() < 60 {
		if tiles := w.City.NearestChargingTiles(c.Pos, 1); len(tiles) > 0 {
			return tiles[0], true
		}
	}
	return engine.Position{}, false
}

// canReachWithCharge and the older helpers were replaced by canCompleteRoute,
// which plans the whole remaining route instead of one leg.

// nextStepToward returns the first direction on a shortest path, or "" when
// already there or unreachable. It is a small BFS so the bot can navigate
// around water without any engine support the player would not also have.
func nextStepToward(w *engine.World, from, to engine.Position) engine.Direction {
	if from == to {
		return ""
	}
	// BFS from `from`, remembering the first direction taken on each branch.
	type qentry struct {
		p     engine.Position
		first engine.Direction
	}
	queue := []qentry{{p: from}}
	seen := map[engine.Position]bool{from: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range engine.AllDirections {
			n, ok := cur.p.Neighbour(d)
			if !ok || seen[n] || !w.City.Walkable(n) {
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
			queue = append(queue, qentry{p: n, first: first})
		}
	}
	return ""
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
		if !c.HasSpace() || c.State(w.City) == engine.CarStranded {
			continue
		}
		if carBusy(w, c) {
			continue
		}
		d := w.City.Distance(c.Pos, k.Pos)
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
	// the spawner caps the queue at ~0.75x capacity, so a struggling fleet never
	// sees a queue big enough to justify growing, and orders expire forever. The
	// honest signals are different: orders are going unclaimed, or deliveries
	// that do happen are slow relative to the queue.
	if len(w.Cars) >= 6 {
		return
	}
	capacity := 0
	for _, c := range w.Cars {
		capacity += c.Class.Capacity
	}
	// Unclaimed orders mean work is piling up that no car is taking.
	unclaimed := 0
	for _, o := range w.OpenOrders() {
		if o.State == engine.OrderPending {
			unclaimed++
		}
	}
	// Expiry pressure means the fleet cannot turn orders over fast enough.
	strained := unclaimed > 0 || w.Ledger.ExpiredOrders > w.Ledger.Deliveries/3
	if !strained && w.ActiveOrderCount() < capacity {
		return
	}
	for _, cc := range w.CarClassCatalog() {
		if cc.Price <= 0 || !cc.Unlocked || !cc.Affordable {
			continue
		}
		if w.Ledger.Money-cc.Price < reserve {
			continue
		}
		if _, err := w.BuyCar(cc.Class, "d_main"); err != nil {
			continue
		}
		return
	}
}

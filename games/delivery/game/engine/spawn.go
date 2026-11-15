package engine

import (
	"fmt"
	"sort"
)

// Order spawning.
//
// Three gates decide whether a customer raises an order on a given tick
// (IDEA.txt §7.1):
//
//	GATE 1  that customer's own cooldown has elapsed
//	GATE 2  the global cap is not reached
//	GATE 3  the queue is below what the fleet can actually handle
//
// GATE 3 is the one that matters. Sizing the queue by customer count would
// flood it as districts unlock; sizing it by fleet capacity keeps supply one
// step behind throughput, which is what makes buying a car feel like progress
// rather than a treadmill.
//
// A fourth rule sits on top: if the queue is empty and no car is carrying
// anything, spawn something. Waiting at the depot with nothing to do is the
// worst possible early-game feel, and cooldowns alone cannot prevent it.

// spawnState is the per-customer memory the spawner needs. It lives on the
// World so it survives across ticks and is saved with the session.
type spawnState struct {
	NextEligibleTick map[string]int64
	LastSpawnTick    int64
	HasSpawned       bool
}

// targetActive is the queue depth the pacing governor aims for.
//
// It is deliberately derived from FLEET SIZE, not from cargo capacity in boxes.
// Sizing it by boxes was the obvious first idea and it is wrong: the moment a
// truck holds forty boxes, a capacity-based target asks for thirty concurrent
// orders, and every number in the tuning table becomes meaningless. Fleet size
// grows in single steps and is the honest measure of how much work the player
// can physically do at once.
func (w *World) targetActive() int {
	ratio := w.ordersCfg.TargetOrdersPerCar
	if ratio <= 0 {
		ratio = 1.0
	}
	driving := 0
	for _, c := range w.Cars {
		if c.State(w.City) != CarStranded {
			driving++
		}
	}
	target := int(float64(driving)*ratio + 0.5) // round half up
	if target < 1 {
		target = 1
	}
	if w.ordersCfg.MaxActiveOrders > 0 && target > w.ordersCfg.MaxActiveOrders {
		target = w.ordersCfg.MaxActiveOrders
	}
	return target
}

// spawnOrders runs step 5 of the tick. It returns nothing; events are emitted.
func (w *World) spawnOrders() {
	// The forced first order: a player must never start in an empty world.
	if !w.spawn.HasSpawned && w.Tick >= int64(w.ordersCfg.ForceFirstOrderAtTick) {
		w.spawnForced()
		return
	}

	active := w.ActiveOrderCount()

	// Anti-dead-air: nothing on the board and nobody carrying anything.
	if active == 0 && w.noCarCarrying() && w.canSpawnAgain() {
		w.spawnOne()
		return
	}

	// GATE 3.
	if active >= w.targetActive() {
		return
	}
	// GATE 2.
	if w.ordersCfg.MaxActiveOrders > 0 && active >= w.ordersCfg.MaxActiveOrders {
		return
	}

	// GATE 1, evaluated per customer in id order for determinism.
	customers := make([]Customer, len(w.City.Customers))
	copy(customers, w.City.Customers)
	sort.Slice(customers, func(i, j int) bool { return customers[i].ID < customers[j].ID })

	for _, cu := range customers {
		if w.Tick < w.spawn.NextEligibleTick[cu.ID] {
			continue
		}
		if w.spawnOneFor(cu) {
			return // at most one new order per tick
		}
	}
}

// spawnForced emits the first order of a session, ignoring gates.
func (w *World) spawnForced() {
	if len(w.City.Customers) == 0 {
		w.spawn.HasSpawned = true
		return
	}
	// Bias the very first job toward the customer nearest the depot, so a new
	// player's first delivery is short enough to teach the loop.
	depotPos := w.City.Depots[0].Pos
	best := w.City.Customers[0]
	bestDist := -1
	for _, cu := range w.City.Customers {
		d := w.City.Distance(depotPos, cu.Pos)
		if d < 0 {
			continue
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = cu, d
		}
	}
	if w.spawnOneFor(best) {
		w.spawn.HasSpawned = true
	}
}

// spawnOne tries every customer in id order until one produces an order.
func (w *World) spawnOne() bool {
	customers := make([]Customer, len(w.City.Customers))
	copy(customers, w.City.Customers)
	sort.Slice(customers, func(i, j int) bool { return customers[i].ID < customers[j].ID })
	for _, cu := range customers {
		if w.spawnOneFor(cu) {
			return true
		}
	}
	return false
}

// canSpawnAgain enforces the anti-dead-air gap.
func (w *World) canSpawnAgain() bool {
	gap := int64(w.ordersCfg.MinSpawnGapTicks)
	if gap <= 0 {
		gap = 60
	}
	return w.Tick >= w.spawn.LastSpawnTick+gap
}

// noCarCarrying reports whether every car has an empty cargo hold.
func (w *World) noCarCarrying() bool {
	for _, c := range w.Cars {
		if len(c.Cargo) > 0 {
			return false
		}
	}
	return true
}

// spawnOneFor tries to raise one order for a customer. It returns false when
// the customer cannot be served right now (no reachable kitchen, no demand
// satisfiable), which is a content or geography fact rather than an error.
func (w *World) spawnOneFor(cu Customer) bool {
	item := w.pickDemand(cu)
	if item == "" {
		return false
	}
	kitchen, ok := w.nearestKitchenFor(cu.Pos, item)
	if !ok {
		// No reachable kitchen serves this item. Do not silently drop it: this
		// is a content bug that the harness should surface.
		w.emit(EventSpawned, nil, fmt.Sprintf("order_starved: %s wants %s but no kitchen can supply it",
			cu.Name, w.itemLabel(item)))
		w.advanceCooldown(cu, item)
		return false
	}

	w.orderSeq++
	o := &Order{
		ID:           fmt.Sprintf("ord_%d", w.orderSeq),
		Item:         item,
		KitchenID:    kitchen.ID,
		CustomerID:   cu.ID,
		Quantity:     w.rollQuantity(cu),
		CreatedTick:  w.Tick,
		DeadlineTick: w.Tick + w.estimateTicks(kitchen.Pos, cu.Pos),
		Penalty:      w.penaltyFor(item),
		State:        OrderPending,
	}
	o.Reward = w.rewardFor(item, kitchen.Pos, cu.Pos, o.Quantity)
	w.Orders = append(w.Orders, o)
	w.Ledger.OrdersSpawned++
	w.spawn.LastSpawnTick = w.Tick
	w.spawn.HasSpawned = true
	w.advanceCooldown(cu, item)

	w.emit(EventSpawned, nil, fmt.Sprintf("%s wants %d× %s from %s ($%d, %d ticks)",
		cu.Name, o.Quantity, w.itemLabel(item), kitchen.Name, o.Reward, o.DeadlineTick-w.Tick))
	return true
}

// rollQuantity picks how many boxes this customer asks for.
//
// Three filters decide the draw, and the fleet filter is the important one:
// a band is only eligible when some car in the fleet can actually carry its
// largest size. Otherwise the spawner would raise a forty-box order for a
// player with one scooter, which is the same class of bug as the harbour
// orders that were sold while the bridge was closed -- a promise the player
// cannot keep. The fallback is always a single box, which every car can take.
func (w *World) rollQuantity(cu Customer) int {
	bands := w.ordersCfg.QuantityBands
	if len(bands) == 0 {
		return 1
	}
	chapter := w.Chapter()
	biggest := w.largestHold()

	type candidate struct {
		band   QuantityBandConfig
		weight int
	}
	var eligible []candidate
	total := 0
	for _, b := range bands {
		if b.MinSize <= 0 || b.MaxSize < b.MinSize {
			continue
		}
		if chapter < b.FromChapter {
			continue
		}
		// Clamp the band to the fleet's largest hold rather than dropping it.
		// Dropping a 20-40 band on a 24-box van would lose the heavy jobs the
		// van can actually do; clamping raises a 20-24 box order instead. A
		// band whose minimum does not fit is dropped outright, because a capped
		// band below its own minimum is not that band any more.
		if b.MinSize > biggest {
			continue
		}
		if b.MaxSize > biggest {
			b.MaxSize = biggest
		}
		weight := b.Weight
		if weight <= 0 {
			weight = 1
		}
		eligible = append(eligible, candidate{band: b, weight: weight})
		total += weight
	}
	if total <= 0 {
		return 1
	}

	roll := Roll(w.Seed, w.Tick, "quantity:"+cu.ID, total)
	for _, c := range eligible {
		if roll < c.weight {
			return RollRange(w.Seed, w.Tick, "quantity-size:"+cu.ID,
				c.band.MinSize, c.band.MaxSize)
		}
		roll -= c.weight
	}
	return eligible[0].band.MinSize
}

// largestHold is the biggest hold in the fleet, in boxes. It ignores stranded
// cars: a car with a flat battery cannot take a job, so including it would let
// the spawner raise work that nothing on the road can accept.
func (w *World) largestHold() int {
	biggest := 0
	for _, c := range w.Cars {
		if c.State(w.City) == CarStranded {
			continue
		}
		if c.Class.Capacity > biggest {
			biggest = c.Class.Capacity
		}
	}
	return biggest
}

// advanceCooldown pushes a customer's next eligibility forward by its own
// interval plus jitter, so orders do not arrive with metronomic regularity.
func (w *World) advanceCooldown(cu Customer, item string) {
	interval := cu.OrderIntervalTicks
	if interval <= 0 {
		interval = 40
	}
	jitter := 0
	if cu.JitterTicks > 0 {
		jitter = RollRange(w.Seed, w.Tick, "jitter:"+cu.ID+":"+item, 0, cu.JitterTicks)
	}
	w.spawn.NextEligibleTick[cu.ID] = w.Tick + int64(interval+jitter)
}

// pickDemand chooses which of a customer's demands to order, deterministically.
func (w *World) pickDemand(cu Customer) string {
	if len(cu.Demands) == 0 {
		return ""
	}
	if len(cu.Demands) == 1 {
		return cu.Demands[0]
	}
	// Only consider items some kitchen can actually supply right now.
	var viable []string
	for _, item := range cu.Demands {
		if _, ok := w.nearestKitchenFor(cu.Pos, item); ok {
			viable = append(viable, item)
		}
	}
	if len(viable) == 0 {
		return ""
	}
	return Pick(w.Seed, w.Tick, "demand:"+cu.ID, viable)
}

// nearestKitchenFor finds the kitchen serving item whose walking distance to
// dest is smallest. Ties break by kitchen id so the choice is reproducible.
func (w *World) nearestKitchenFor(dest Position, item string) (Kitchen, bool) {
	var best Kitchen
	bestDist := -1
	for _, k := range w.City.Kitchens {
		if !k.Serves(item) || !w.City.Walkable(k.Pos) {
			continue
		}
		d := w.City.Distance(k.Pos, dest)
		if d < 0 {
			continue
		}
		if bestDist < 0 || d < bestDist || (d == bestDist && k.ID < best.ID) {
			best, bestDist = k, d
		}
	}
	return best, bestDist >= 0
}

// estimateTicks is how long a competent driver needs, used to derive a
// deadline. It is the route length times the deadline slack.
func (w *World) estimateTicks(kitchenPos, customerPos Position) int64 {
	var fromPos Position
	if len(w.City.Depots) > 0 {
		fromPos = w.City.Depots[0].Pos
	}
	return w.estimateFrom(fromPos, kitchenPos, customerPos)
}

// estimateFrom is the deadline window for a courier starting at from: the
// drive to the kitchen plus the drive to the customer, scaled by slack.
func (w *World) estimateFrom(from, kitchenPos, customerPos Position) int64 {
	slack := w.deadlineSlack()

	total := 0
	if d := w.City.Distance(from, kitchenPos); d > 0 {
		total += d
	}
	if d := w.City.Distance(kitchenPos, customerPos); d > 0 {
		total += d
	} else if d < 0 {
		total += 20 // unreachable at estimate time: assume a long haul
	}
	if total < 5 {
		total = 5
	}
	est := float64(total) * slack
	if est < 10 {
		est = 10
	}
	return int64(est + 0.5)
}

// deadlineSlack returns the current slack multiplier, tightening by chapter.
func (w *World) deadlineSlack() float64 {
	slack := w.ordersCfg.DeadlineSlack
	if slack <= 0 {
		slack = 2.0
	}
	byChapter := w.ordersCfg.DeadlineSlackByChapter
	if len(byChapter) > 0 {
		ch := w.Chapter()
		if ch >= len(byChapter) {
			ch = len(byChapter) - 1
		}
		if ch >= 0 {
			slack = byChapter[ch]
		}
	}
	return slack
}

// rewardFor computes pay: base by item, scaled by chapter, plus a distance
// bonus so long hauls are worth the battery.
//
// Quantity scales pay sublinearly on purpose. Linear pay per box makes bulk
// strictly better -- forty boxes at the single-box rate would out-earn any
// number of small runs and make order choice a non-decision. The exponent is
// what keeps a full truck a good job and a small van job still worth taking.
func (w *World) rewardFor(item string, kitchenPos, customerPos Position, quantity int) int {
	base := w.ordersCfg.RewardBase[item]
	if base <= 0 {
		base = 40
	}
	if quantity <= 0 {
		quantity = 1
	}
	mult := 1.0
	if m := w.ordersCfg.ChapterRewardMultiplier; len(m) > 0 {
		ch := w.Chapter()
		if ch >= len(m) {
			ch = len(m) - 1
		}
		mult = m[ch]
	}
	// 0.6 + 0.4*qty gives 1.0x at one box and 16.6x at forty, i.e. $1600
	// rather than $1600-per-box-times-forty.
	bulk := 0.6 + 0.4*float64(quantity)
	reward := float64(base) * mult * bulk
	if dist := w.City.Distance(kitchenPos, customerPos); dist > 0 {
		reward += float64(dist * w.ordersCfg.DistanceBonusPerTile)
	}
	return int(reward + 0.5)
}

// penaltyFor is what an expired order costs. Small: expiry should sting, not
// bankrupt.
func (w *World) penaltyFor(item string) int {
	base := w.ordersCfg.RewardBase[item]
	if base <= 0 {
		base = 40
	}
	return base / 4
}

// Chapter derives the progression chapter from builds completed, which is the
// honest measure of how far the city has grown.
func (w *World) Chapter() int {
	ch := w.Ledger.BuildsCompleted
	if ch > MaxChapter {
		ch = MaxChapter
	}
	if ch < 0 {
		ch = 0
	}
	return ch
}

// MaxChapter caps the chapter index used for tuning tables.
const MaxChapter = 4

package engine

import (
	"fmt"
	"sort"
)

// Order spawning.
//
// The tick's spawn decision runs in this order, and the order matters because
// every branch returns:
//
//	0. forced first order — until HasSpawned, one order is raised regardless
//	   of every gate, so a player never starts in an empty world
//	1. anti-dead-air — if nothing is live, nobody is carrying anything, and
//	   MinSpawnGapTicks has elapsed, raise one immediately
//	2. GATE 3 — the queue is below what the fleet can handle (targetActive)
//	3. GATE 2 — the global cap MaxActiveOrders is not reached
//	4. GATE 1 — per customer, in id order, that customer's cooldown has elapsed
//
// Note that GATE 3 is checked BEFORE GATE 2 and GATE 1, not after: once the
// queue is full by fleet capacity, no customer is even considered. Sizing the
// queue by customer count would flood it as districts unlock; sizing it by fleet
// capacity keeps supply one step behind throughput, which is what makes buying a
// car feel like progress rather than a treadmill.
//
// At most one order is raised per tick, and the customer loop is sorted by id so
// a replay of (seed, commands) raises the same order for the same customer.
//
// Two different questions gate an individual customer, and they are NOT
// symmetric:
//
//	customerIsServable  is ANY demand reachable right now? If not, the
//	                    customer is skipped silently and its cooldown advanced.
//	pickDemand          which demand to roll. For a single-demand customer this
//	                    returns that demand unconditionally; only a
//	                    multi-demand customer is filtered to viable items.
//
// Because spawnOneFor returns on customerIsServable before pickDemand runs, the
// nearestKitchenFor failure branch further down is unreachable dead code. See its
// comment for the measured cases, and docs/known-issues.md #4 for the
// consequence: an unsupplied customer is silent rather than diagnosed.

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
		d := w.routeDistance(depotPos, cu.Pos)
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
	// A customer cut off from the fleet must not raise work: a closure, a
	// fogged district or a stranded fleet would otherwise sell orders that
	// expire unsold. This is the check whose absence produced 72 impossible
	// orders out of 175 with the bridge shut.
	if !w.customerIsServable(cu) {
		w.advanceCooldown(cu, "")
		return false
	}

	item := w.pickDemand(cu)
	if item == "" {
		return false
	}
	kitchen, ok := w.nearestKitchenFor(cu.Pos, item)
	if !ok {
		// Unreachable in practice, and that is worth stating rather than
		// pretending this is a live diagnostic. Reaching it needs
		// customerIsServable(cu) == true together with nearestKitchenFor failing
		// for the picked item. For a single-demand customer pickDemand returns
		// that demand with NO supply check, while customerIsServable asked the
		// same question about the same item — so both agree and one of them
		// already returned. For a multi-demand customer pickDemand has already
		// filtered out every item no kitchen can supply, so the lookup cannot
		// fail either. Measured: clearing all menus gives
		// single-demand {"pizza"} -> servable=false, pickDemand="pizza"
		// multi-demand {pizza,sushi} -> servable=false, pickDemand="".
		//
		// The upshot is that an unsupplied customer is SILENT: no order, no
		// event. TestEveryCustomerHasASupplyingKitchen is what actually catches
		// a bad menu, at authoring time.
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

// customerIsServable reports whether the fleet could actually complete a job
// for this customer right now.
//
// "Servable" is deliberately about geography, not battery or spare capacity:
// a customer on the far side of the map with a car parked at the depot is a
// legitimate job, while a customer behind a road closure is not. Hidden
// districts are excluded because they are not yet in play.
func (w *World) customerIsServable(cu Customer) bool {
	if w.City.IsHidden(cu.Pos) {
		return false
	}
	if w.ClosedAt(cu.Pos) {
		return false
	}
	// A customer needs one reachable kitchen and a depot/charger that can
	// reach both, which is exactly what routeDistance answers once closures
	// are part of the routing.
	for _, item := range cu.Demands {
		if _, ok := w.nearestKitchenFor(cu.Pos, item); ok {
			return true
		}
	}
	return false
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
		d := w.routeDistance(k.Pos, dest)
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

// estimateFrom is the tick window a job from `from` would need: drive to the
// kitchen, then to the customer, with slack applied.
//
// Each leg is routed (closure-aware). A leg of 0 is legitimate -- the car may
// already be standing on the kitchen -- so it must be added, not skipped. A leg
// of -1 means no route exists, and both legs answer that with the same flat
// +20 long-haul assumption.
//
// Neither leg can be -1 merely because the car stands on a closed tile: a
// closure blocks ENTRY, so DistanceAvoiding treats the origin as passable.
// That is what keeps this window identical either side of a closure closing
// under the car (measured 16 ticks both ways on the shipped map). Before that
// was fixed this function skipped a negative first leg and quoted a SHORTER
// deadline, which is `drbrain-delivery-bic` in docs/known-issues.md.
//
// Slack comes from OrderConfig.DeadlineSlack and tightens by chapter. Note the
// zero-value fallback is 2.0, not the 1.8 the shipped map uses, so a map that
// omits deadline_slack is more generous than city.json rather than identical.
func (w *World) estimateFrom(from, kitchenPos, customerPos Position) int64 {
	slack := w.deadlineSlack()

	total := 0
	if d := w.routeDistance(from, kitchenPos); d >= 0 {
		total += d
	} else {
		total += 20 // kitchen unreachable from here: assume a long haul
	}
	if d := w.routeDistance(kitchenPos, customerPos); d >= 0 {
		total += d
	} else {
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
// Quantity scales pay sublinearly on purpose: 0.6 + 0.4*qty is affine, not
// linear and not exponential, so the per-box rate falls as the order grows.
// Linear pay per box would make bulk strictly better -- forty boxes at the
// single-box rate would out-earn any number of small runs and make order choice
// a non-decision. The shape is what keeps a full truck a good job while a small
// van job is still worth taking.
//
// Measured on the shipped map for pizza (base 40, 6-tile haul, chapter 0):
//
//	qty  1 ->  58   (58.0 per box)
//	qty 10 -> 202   (20.2 per box)
//	qty 40 -> 682   (17.1 per box)
//
// The distance bonus is flat, so on short hauls the per-box rate is dominated by
// the base multiplier; it is only on the long ones that distance shows.
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
	// Affine, not exponential: 1.0x at one box, 16.6x at forty, so a full truck
	// pays 682 on the shipped map rather than 40x the single-box rate.
	bulk := 0.6 + 0.4*float64(quantity)
	reward := float64(base) * mult * bulk
	if dist := w.routeDistance(kitchenPos, customerPos); dist > 0 {
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

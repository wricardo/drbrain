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
//	2. GATE 3 — the queue is below the demand target (visible customers plus
//	   the unbounded TargetGrowthPer1000Ticks term)
//	3. GATE 2 — a positive MaxActiveOrders is not reached (zero means no cap)
//	4. GATE 1 — one site is drawn at random from those whose cooldown has
//	   elapsed and that have no live order
//
// Note that GATE 3 is checked BEFORE GATE 2 and GATE 1, not after: once the
// queue is at the demand target, no customer is even considered. Demand is
// sized by the visible customer population, not by the player's fleet: buying a
// car must add throughput, not create the work that justifies itself. A growing
// city adds customers on its own schedule, so the fleet has to catch up or
// orders expire. Once the schedule ends, TargetGrowthPer1000Ticks keeps raising
// the target so the queue does not settle at a constant depth.
//
// At most one order is raised per tick. The eligible sites are sorted by id and
// the winner is picked with Roll(seed, tick, "customer", len), so a replay of
// (seed, commands) raises the same order for the same customer, while a player
// does not see the alphabetically first customer win every time.
//
// Customers are not permanently on the map: a site is "present" only while it
// has a live order (see customerPresent), so it disappears when that order is
// delivered or expires. Sites in hidden districts are not eligible, which is
// what makes a reveal build expose the customers of the new district.
//
// Two different questions gate an individual customer, and they are NOT
// symmetric:
//
//	customerIsServable  is ANY demand reachable right now? If not, the
//	                    customer is skipped silently and reconsidered on the
//	                    next spawn (dedicated cooldowns track time, not
//	                    geography).
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
// Demand is anchored to the visible customer population, not to the player's
// fleet. Sizing it by the fleet made buying a car raise the target at the same
// moment it added throughput: the queue treadmill moved with the player and a
// single scooter could keep up forever. Customers are the world's demand; a
// growing city adds them on its own schedule, and the player has to buy enough
// fleet (or a big enough vehicle) to serve them before deadlines expire.
//
// The chapter floor keeps a very small map from going quiet after a growth
// stage; MaxActiveOrders is still the hard ceiling when the map sets one.
// TargetGrowthPer1000Ticks is the only term that never settles, so a long
// session keeps raising the bar after the last customer appears.
func (w *World) targetActive() int {
	ratio := w.ordersCfg.TargetOrdersPerCustomer
	if ratio <= 0 {
		ratio = 0.75
	}
	visible := 0
	for _, cu := range w.City.Customers {
		if !w.City.IsHidden(cu.Pos) {
			visible++
		}
	}
	target := int(float64(visible)*ratio + 0.5) // round half up
	if target < 1 {
		target = 1
	}
	// Growth raises the floor gradually even when a map adds no customers in
	// a stage; the customer count remains the primary signal.
	if ch := w.Chapter(); (ch+1)/2 > target {
		target = (ch + 1) / 2
	}
	// The unbounded term: after the schedule and ramp end, the queue target
	// keeps rising with the clock. This is what stops a finished map from
	// settling at round(visible × ratio) forever.
	if g := w.ordersCfg.TargetGrowthPer1000Ticks; g > 0 {
		target += int(float64(w.Tick) * g / 1000)
	}
	// A positive MaxActiveOrders stays a hard ceiling; zero means no cap.
	if w.ordersCfg.MaxActiveOrders > 0 && target > w.ordersCfg.MaxActiveOrders {
		target = w.ordersCfg.MaxActiveOrders
	}
	return target
}

// TargetActive exposes the governor's current queue target for the simulation
// harness and tests. Gameplay reads targetActive; this is the same number, not
// a second derivation.
func (w *World) TargetActive() int { return w.targetActive() }

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
	// GATE 2: only a positive cap binds; zero means the target is the bound.
	if w.ordersCfg.MaxActiveOrders > 0 && active >= w.ordersCfg.MaxActiveOrders {
		return
	}

	// GATE 1: at most one order per tick, from a random eligible site.
	w.spawnOne()
}

// spawnForced emits the first order of a session, ignoring gates.
//
// Among the eligible sites it still picks the one nearest the depot, so a new
// player's first delivery is short enough to teach the loop; the randomness of
// ordinary spawns starts after it.
func (w *World) spawnForced() {
	eligible := w.eligibleCustomers()
	if len(eligible) == 0 {
		// Nothing can be served yet: every site is hidden, cut off, or there is
		// no depot to measure from. Stop retrying the forced branch every tick;
		// the anti-dead-air branch raises the first order once a reveal makes a
		// site eligible.
		w.spawn.HasSpawned = true
		return
	}

	best := eligible[0]
	if len(w.City.Depots) > 0 {
		depotPos := w.City.Depots[0].Pos
		bestDist := -1
		for _, cu := range eligible {
			d := w.routeDistance(depotPos, cu.Pos)
			if d < 0 {
				continue
			}
			if bestDist < 0 || d < bestDist || (d == bestDist && cu.ID < best.ID) {
				best, bestDist = cu, d
			}
		}
	}
	w.spawnOneFor(best)
	w.spawn.HasSpawned = true
}

// spawnOne raises a single order for a randomly chosen eligible site.
func (w *World) spawnOne() bool {
	cu, ok := w.pickSpawnCustomer()
	if !ok {
		return false
	}
	return w.spawnOneFor(cu)
}

// pickSpawnCustomer draws the site that raises the next order.
//
// Eligibility is decided by eligibleCustomers; the draw is uniform over that
// id-sorted list. Because the roll derives from (seed, tick, "customer"), the
// result is reproducible for a replayed save while removing the id-order bias
// that used to let one customer win every spawn.
func (w *World) pickSpawnCustomer() (Customer, bool) {
	eligible := w.eligibleCustomers()
	if len(eligible) == 0 {
		return Customer{}, false
	}
	return eligible[Roll(w.Seed, w.Tick, "customer", len(eligible))], true
}

// eligibleCustomers returns the sites that may raise an order right now.
//
// A site is eligible when it is not present (no live order — see
// customerPresent), its cooldown has elapsed, and it is servable: revealed, not
// inside a closure, and with a reachable kitchen for some demand. The hidden
// check inside customerIsServable is what keeps customers of a locked district
// off the board until a growth stage reveals it.
//
// The result is sorted by id, not left in city-file order, because growth
// stages append customers to the city as they apply. The draw indexes this
// slice, so an unsorted list would make order spawning depend on append order
// rather than on stable content.
func (w *World) eligibleCustomers() []Customer {
	out := make([]Customer, 0, len(w.City.Customers))
	for _, cu := range w.City.Customers {
		if w.customerPresent(cu.ID) {
			continue
		}
		if w.Tick < w.spawn.NextEligibleTick[cu.ID] {
			continue
		}
		if !w.customerIsServable(cu) {
			continue
		}
		out = append(out, cu)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// customerPresent reports whether a site currently has a live order.
//
// Presence is derived from the order list rather than stored: a customer is on
// the map exactly while it is waiting for a delivery, so delivering or expiring
// its order removes it with no second piece of state to keep in sync (and no
// extra field in the save format).
func (w *World) customerPresent(id string) bool {
	for _, o := range w.Orders {
		if !o.State.Terminal() && o.CustomerID == id {
			return true
		}
	}
	return false
}

// PresentCustomers returns the sites that are waiting for a delivery right now,
// in city order. This is what the client should draw as customers; the house
// tiles themselves are permanent map features.
func (w *World) PresentCustomers() []Customer {
	out := make([]Customer, 0, len(w.City.Customers))
	if len(w.City.Customers) == 0 {
		return out
	}
	present := make(map[string]bool)
	for _, o := range w.Orders {
		if !o.State.Terminal() {
			present[o.CustomerID] = true
		}
	}
	for _, cu := range w.City.Customers {
		if present[cu.ID] {
			out = append(out, cu)
		}
	}
	return out
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
// Progress is the only filter. Demand is intentionally allowed to exceed the
// current fleet: a 13-box job in a city where the player owns only a 4-box
// scooter is the signal that the van is now worth buying. The assignment path
// still refuses to exceed a car's hold, so an oversized order cannot corrupt
// cargo; it sits pending (and eventually expires) until a capable vehicle
// exists. The fallback is always a single box, which every car can take.
//
// A band with EarlyWeight stays in the draw from tick 0 at that reduced share;
// its Weight takes over at FromProgress. That is what keeps every quantity
// range, and therefore every vehicle class, relevant at every stage instead of
// switching the small bands off once the ramp finishes.
func (w *World) rollQuantity(cu Customer) int {
	bands := w.ordersCfg.QuantityBands
	if len(bands) == 0 {
		return 1
	}
	progress := w.Progress()

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
		weight := b.Weight
		if progress < b.FromProgress {
			weight = b.EarlyWeight
		}
		if weight <= 0 {
			continue
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

// advanceCooldown pushes a customer's next eligibility forward by its own
// interval plus jitter. Both are scaled by ramp progress, so order frequency
// grows over the session instead of stepping at a chapter boundary. After the
// ramp, CooldownDecayPer1000Ticks keeps shaving the interval down to a 1-tick
// floor; that is the arrival-rate half of the endless difficulty curve.
func (w *World) advanceCooldown(cu Customer, item string) {
	interval := cu.OrderIntervalTicks
	if interval <= 0 {
		interval = 40
	}
	minScale := w.ordersCfg.CustomerCooldownMinScale
	if minScale <= 0 || minScale > 1 {
		minScale = 0.5
	}
	scale := 1 - (1-minScale)*w.Progress()
	interval = int(float64(interval)*scale + 0.5)
	if d := w.ordersCfg.CooldownDecayPer1000Ticks; d > 0 {
		interval -= int(float64(w.Tick) * d / 1000)
	}
	if interval < 1 {
		interval = 1
	}
	jitter := 0
	if cu.JitterTicks > 0 {
		jitter = int(float64(RollRange(w.Seed, w.Tick, "jitter:"+cu.ID+":"+item, 0, cu.JitterTicks))*scale + 0.5)
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
// of -1 means no route exists, and both legs answer that with the same
// unreachable_leg_ticks long-haul assumption.
//
// Neither leg can be -1 merely because the car stands on a closed tile: a
// closure blocks ENTRY, so DistanceAvoiding treats the origin as passable.
// That is what keeps this window identical either side of a closure closing
// under the car (measured 16 ticks both ways on the shipped map).
//
// The older shape skipped a negative first leg instead of substituting the
// long-haul value, which quoted a SHORTER deadline for exactly that case — the
// opposite of what a closure should do. Keep the two legs symmetric: any
// special case applied to one must be applied to the other.
//
// Slack comes from OrderConfig.DeadlineSlack and tightens by chapter.
func (w *World) estimateFrom(from, kitchenPos, customerPos Position) int64 {
	slack := w.deadlineSlack()

	total := 0
	for _, leg := range [2]int{
		w.routeDistance(from, kitchenPos),
		w.routeDistance(kitchenPos, customerPos),
	} {
		if leg >= 0 {
			total += leg
		} else {
			total += int(w.ordersCfg.UnreachableLegTicks)
		}
	}
	if int64(total) < w.ordersCfg.MinRouteTicks {
		total = int(w.ordersCfg.MinRouteTicks)
	}
	est := float64(total) * slack
	if est < float64(w.ordersCfg.MinDeadlineTicks) {
		est = float64(w.ordersCfg.MinDeadlineTicks)
	}
	return int64(est + 0.5)
}

// deadlineSlack returns the current slack multiplier, tightening by chapter.
func (w *World) deadlineSlack() float64 {
	slack := w.ordersCfg.DeadlineSlack
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
// Quantity scales pay sublinearly on purpose: RewardBulkBase +
// RewardBulkPerBox*qty is affine, not linear and not exponential, so the
// per-box rate falls as the order grows. Linear pay per box would make bulk
// strictly better -- forty boxes at the single-box rate would out-earn any
// number of small runs and make order choice a non-decision. The shape is what
// keeps a full truck a good job while a small van job is still worth taking.
//
// Measured on the shipped map for pizza (base 40, 6-tile haul, chapter 0,
// bulk base 0.6, per box 0.4):
//
//	qty  1 ->  58   (58.0 per box)
//	qty 10 -> 202   (20.2 per box)
//	qty 40 -> 682   (17.1 per box)
//
// The distance bonus is flat, so on short hauls the per-box rate is dominated by
// the base multiplier; it is only on the long ones that distance shows.
func (w *World) rewardFor(item string, kitchenPos, customerPos Position, quantity int) int {
	base := w.ordersCfg.RewardBase[item]
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
	bulk := w.ordersCfg.RewardBulkBase + w.ordersCfg.RewardBulkPerBox*float64(quantity)
	reward := float64(base) * mult * bulk
	if dist := w.routeDistance(kitchenPos, customerPos); dist > 0 {
		reward += float64(dist * w.ordersCfg.DistanceBonusPerTile)
	}
	return int(reward + 0.5)
}

// penaltyFor is what an expired order costs. Small: expiry should sting, not
// bankrupt. The divisor is required map config; there is no fallback.
func (w *World) penaltyFor(item string) int {
	return w.ordersCfg.RewardBase[item] / w.ordersCfg.ExpireFineDivisor
}

// Progress is how far through the demand ramp the session is, in [0,1].
// Quantity bands and customer cooldowns scale from this percentage, not from
// the number of growth stages that happen to have applied.
func (w *World) Progress() float64 {
	if w.ordersCfg.RampTicks <= 0 {
		return 1
	}
	p := float64(w.Tick) / float64(w.ordersCfg.RampTicks)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// Chapter derives the progression chapter from how many growth stages have
// applied. The city's own timeline is the honest measure of how far it has
// grown; no player action is involved.
func (w *World) Chapter() int {
	ch := w.growthApplied
	if ch > w.maxChapter {
		ch = w.maxChapter
	}
	if ch < 0 {
		ch = 0
	}
	return ch
}

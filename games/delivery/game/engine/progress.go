package engine

import (
	"fmt"
	"sort"
)

// City growth.
//
// The city expands on a schedule, not through purchases: each stage names a
// tick and a list of effects, and the world applies it the first time the clock
// reaches that tick. A stage is atomic — districts reveal together, a bridge
// and its charger appear together — and rows that would strand a kitchen or a
// customer are rejected rather than partially applied.
//
// Chapter() is derived from how many stages have applied, so the pacing tables
// (quantity bands, reward multipliers, deadline slack) intensify with the city
// itself rather than with anything the player buys.

// GrowthStatus is the per-stage view the API exposes.
type GrowthStatus struct {
	ID          string
	Tick        int64
	Title       string
	Description string
	Done        bool
}

// Growth returns the schedule with its current status, in application order.
func (w *World) Growth() []GrowthStatus {
	out := make([]GrowthStatus, 0, len(w.growth))
	for i, st := range w.growth {
		out = append(out, GrowthStatus{
			ID:          st.ID,
			Tick:        st.Tick,
			Title:       st.Title,
			Description: st.Description,
			Done:        i < w.growthApplied,
		})
	}
	return out
}

// applyGrowth applies every stage whose tick has been reached, in schedule
// order. It runs once per tick, before orders are spawned, so a stage that
// reveals a district can produce work on the same tick the player sees it.
//
// A stage that fails validation at runtime (which authoring validation should
// have caught) is skipped with an event rather than retried forever; the rest of
// the schedule still applies.
func (w *World) applyGrowth() {
	for w.growthApplied < len(w.growth) {
		st := w.growth[w.growthApplied]
		if w.Tick < st.Tick {
			return
		}
		next := w.City.Clone()
		if err := next.applyEffects(st.Effects); err != nil {
			w.growthApplied++
			w.emit(EventGrowth, nil, fmt.Sprintf("%s could not be applied: %v", st.Title, err))
			continue
		}
		if stranded := next.StrandedEntities(); len(stranded) > 0 {
			w.growthApplied++
			w.emit(EventGrowth, nil, fmt.Sprintf("%s would strand %v", st.Title, stranded))
			continue
		}

		w.City = next
		w.applyGrowthGrants(st)
		w.growthApplied++
		w.emit(EventGrowth, nil, fmt.Sprintf("%s: %s", st.Title, st.Description))
	}
}

// applyGrowthGrants handles the non-geographic half of a stage: money and
// reputation grants, and car classes licensed for purchase.
func (w *World) applyGrowthGrants(st GrowthConfig) {
	for _, e := range st.Effects {
		switch e.Type {
		case EffectGrantMoney:
			w.recordCash(CashGrant, e.Amount, nil, "", st.Title+" grant")
			w.Ledger.MoneyEarned += e.Amount
		case EffectGrantRep:
			w.recordReputation(e.Rep, ReputationGrowth, "", st.Title)
		case EffectGrantCarClass:
			if e.Class != "" {
				w.buyableClasses[e.Class] = true
			}
		}
	}
}

// validateGrowthSchedule proves the whole schedule can run in order before a
// session starts: ticks must be non-decreasing, ids unique, and every stage's
// effects must apply to the city the previous stages leave behind without
// stranding a kitchen or customer.
//
// This runs at load, so a bad content edit fails startup naming the stage
// instead of silently skipping half the city's growth twenty minutes into a
// game.
func (w *World) validateGrowthSchedule() error {
	seen := map[string]bool{}
	lastTick := int64(0)
	city := w.City.Clone()
	for i, st := range w.growth {
		if st.ID == "" {
			return fmt.Errorf("growth stage %d has no id", i)
		}
		if seen[st.ID] {
			return fmt.Errorf("duplicate growth stage id %q", st.ID)
		}
		seen[st.ID] = true
		if i > 0 && st.Tick < lastTick {
			return fmt.Errorf("growth stage %q is scheduled at tick %d, before the previous stage's %d",
				st.ID, st.Tick, lastTick)
		}
		lastTick = st.Tick

		next := city.Clone()
		if err := next.applyEffects(st.Effects); err != nil {
			return fmt.Errorf("growth stage %q: %w", st.ID, err)
		}
		if stranded := next.StrandedEntities(); len(stranded) > 0 {
			return fmt.Errorf("growth stage %q would strand %v", st.ID, stranded)
		}
		city = next
	}
	return nil
}

// districtHidden reports whether every tile of a district is still hidden.
func (w *World) districtHidden(d District) bool {
	for _, r := range d.Rects {
		for y := r[1]; y <= r[3]; y++ {
			for x := r[0]; x <= r[2]; x++ {
				if y < 0 || y >= w.City.GridSize || x < 0 || x >= w.City.GridSize {
					continue
				}
				if !w.City.Hidden[y][x] {
					return false
				}
			}
		}
	}
	return true
}

// kitchenByID and customerByID resolve entity ids, used by deadline refreshes.
func (w *World) kitchenByID(id string) (Kitchen, bool) {
	for _, k := range w.City.Kitchens {
		if k.ID == id {
			return k, true
		}
	}
	return Kitchen{}, false
}

func (w *World) customerByID(id string) (Customer, bool) {
	for _, c := range w.City.Customers {
		if c.ID == id {
			return c, true
		}
	}
	return Customer{}, false
}

// CarClassCatalog describes every vehicle archetype with its per-session
// availability, ordered cheapest first so a client renders a stable list.
//
// Prices live in the city file, so the UI must read them from here rather than
// hardcode them: a stale client would otherwise quote a cost the server does
// not charge.
func (w *World) CarClassCatalog() []CarClassInfo {
	out := make([]CarClassInfo, 0, len(w.carClasses))
	for name, cc := range w.carClasses {
		out = append(out, CarClassInfo{
			Class:      name,
			Price:      cc.Price,
			Capacity:   cc.Capacity,
			MaxBattery: cc.MaxBattery,
			StepCost:   cc.StepCost,
			Unlocked:   w.BuyCarClass(name) == nil,
			Affordable: w.Ledger.Money >= cc.Price,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Price != out[j].Price {
			return out[i].Price < out[j].Price
		}
		return out[i].Class < out[j].Class
	})
	return out
}

// CarClassInfo is one entry of the purchasable catalogue. Unlocked and
// Affordable are independent answers: a locked class can still be affordable,
// and an unlocked class can be too expensive.
type CarClassInfo struct {
	// Class is the catalogue key that BuyCar and grant_car_class use.
	Class string
	// Price is the purchase price in dollars.
	Price int
	// Capacity is the cargo hold in boxes, not orders.
	Capacity int
	// MaxBattery is a full charge in battery units.
	MaxBattery int
	// StepCost is the battery spent entering a road tile; ground adds
	// City.GroundExtraStepCost.
	StepCost int
	// Unlocked is the grant rule: true when the class declares no
	// requires_grant lock, or a growth stage has licensed it. It says nothing
	// about the balance.
	Unlocked bool
	// Affordable is Money >= Price when the catalogue was built. It ignores
	// the grant lock, so a caller needs both flags to enable a purchase.
	Affordable bool
}

// BuyCarClass checks whether class is available to buy, without spending
// money. It returns nil when the class exists and its requires_grant lock has
// been satisfied by a growth stage; unknown or still-locked classes return an
// error naming them. Affordability is not checked here (see
// CarClassInfo.Affordable); BuyCar is the call that charges the price.
//
// The rule is stated by the map, not inferred: a class is locked only when it
// declares requires_grant and no growth stage has licensed it yet. Inferring
// the lock from Price was the previous rule, and it silently produced dead
// content — priced classes that nothing could ever unlock.
func (w *World) BuyCarClass(class string) error {
	cc, ok := w.carClasses[class]
	if !ok {
		return fmt.Errorf("unknown car class %q", class)
	}
	if !cc.RequiresGrant {
		return nil
	}
	if w.buyableClasses[class] {
		return nil
	}
	return fmt.Errorf("car class %q is not unlocked yet", class)
}

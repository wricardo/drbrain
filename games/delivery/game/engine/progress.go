package engine

import (
	"fmt"
	"sort"
)

// Progression: achievements fire on their own, unlocks grant permission, and
// builds cost money and change the map.
//
// The separation matters for the player's experience of "the map evolves":
// an achievement is a surprise, an unlock is an offer, and a build is a
// decision. Collapsing them into one event would turn the build into a
// cutscene.

// AchievementStatus is the per-achievement view the API exposes.
type AchievementStatus struct {
	ID          string
	Title       string
	Description string
	Done        bool
	Progress    int // coarse progress toward the strictest requirement
	Target      int // the target that Progress is measured against
}

// UnlockStatus is the per-unlock view the API exposes.
type UnlockStatus struct {
	ID          string
	Title       string
	Description string
	Earned      bool
	Builds      []string
}

// BuildStatus is the per-build view the API exposes.
type BuildStatus struct {
	ID          string
	Title       string
	Description string
	Cost        int
	Unlocked    bool
	Completed   bool
	Affordable  bool
	Applicable  bool   // a legal placement exists right now
	Problem     string // why it cannot be built, when it cannot
}

// evalProgression runs at the end of each tick: achievements and unlocks are
// one-shot, so anything already earned is skipped cheaply.
func (w *World) evalProgression() {
	for i := range w.achievements {
		a := &w.achievements[i]
		if a.Done {
			continue
		}
		if !w.meets(a.Requires) {
			continue
		}
		a.Done = true
		if a.GrantMoney != 0 {
			w.Ledger.Money += a.GrantMoney
			w.Ledger.MoneyEarned += a.GrantMoney
		}
		if a.GrantRep != 0 {
			w.Ledger.Reputation += a.GrantRep
			w.clampReputation()
		}
		msg := "achievement: " + a.Title
		if a.GrantMoney != 0 {
			msg += fmt.Sprintf(" (+$%d)", a.GrantMoney)
		}
		w.emit(EventUnlock, nil, msg)
	}

	for i := range w.unlocks {
		u := &w.unlocks[i]
		if u.Earned {
			continue
		}
		if !w.meets(u.Requires) {
			continue
		}
		u.Earned = true
		for _, bid := range u.GrantsBuild {
			w.permitted[bid] = true
		}
		w.emit(EventUnlock, nil, "unlocked: "+u.Title)
	}
}

// meets evaluates a requirement against the ledger and city. All populated
// fields must hold (AND).
func (w *World) meets(r Requirement) bool {
	if r.Deliveries != nil && w.Ledger.Deliveries < *r.Deliveries {
		return false
	}
	if r.MoneyEarned != nil && w.Ledger.MoneyEarned < *r.MoneyEarned {
		return false
	}
	if r.MoneyBalance != nil && w.Ledger.Money < *r.MoneyBalance {
		return false
	}
	if r.Reputation != nil && w.Ledger.Reputation < *r.Reputation {
		return false
	}
	if r.Tows != nil && w.Ledger.Tows < *r.Tows {
		return false
	}
	if r.ExpiredOrders != nil && w.Ledger.ExpiredOrders < *r.ExpiredOrders {
		return false
	}
	if r.OnTimeStreak != nil && w.Ledger.OnTimeStreak < *r.OnTimeStreak {
		return false
	}
	if r.CarsOwned != nil && len(w.Cars) < *r.CarsOwned {
		return false
	}
	if r.BuildsCompleted != nil && w.Ledger.BuildsCompleted < *r.BuildsCompleted {
		return false
	}
	if r.TilesExploredPct != nil {
		total := w.City.GridSize * w.City.GridSize
		explored := total - w.City.HiddenCount()
		pct := 0
		if total > 0 {
			pct = explored * 100 / total
		}
		if pct < *r.TilesExploredPct {
			return false
		}
	}
	if r.ItemDelivered != nil {
		if r.ItemDelivered.Delivered != nil {
			if w.Ledger.DeliveriesByItem[r.ItemDelivered.Item] < *r.ItemDelivered.Delivered {
				return false
			}
		}
	}
	if r.DistrictsRevealed != nil {
		revealed := 0
		for _, d := range w.City.Districts {
			if len(d.Rects) > 0 && !w.districtHidden(d) {
				revealed++
			}
		}
		if revealed < *r.DistrictsRevealed {
			return false
		}
	}
	return true
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

// Achievements returns the achievement list with status.
func (w *World) Achievements() []AchievementStatus {
	out := make([]AchievementStatus, 0, len(w.achievements))
	for _, a := range w.achievements {
		st := AchievementStatus{
			ID: a.ID, Title: a.Title, Description: a.Description, Done: a.Done,
		}
		st.Progress, st.Target = w.progressFor(a.Requires)
		out = append(out, st)
	}
	return out
}

// progressFor picks the requirement furthest from satisfied and returns
// current/target for it, so a UI can show "3/5 deliveries".
func (w *World) progressFor(r Requirement) (progress, target int) {
	type pair struct{ cur, want int }
	var pairs []pair
	add := func(cur, want int) { pairs = append(pairs, pair{cur, want}) }

	if r.Deliveries != nil {
		add(w.Ledger.Deliveries, *r.Deliveries)
	}
	if r.MoneyEarned != nil {
		add(w.Ledger.MoneyEarned, *r.MoneyEarned)
	}
	if r.MoneyBalance != nil {
		add(w.Ledger.Money, *r.MoneyBalance)
	}
	if r.Tows != nil {
		add(w.Ledger.Tows, *r.Tows)
	}
	if r.ExpiredOrders != nil {
		add(w.Ledger.ExpiredOrders, *r.ExpiredOrders)
	}
	if r.OnTimeStreak != nil {
		add(w.Ledger.OnTimeStreak, *r.OnTimeStreak)
	}
	if r.CarsOwned != nil {
		add(len(w.Cars), *r.CarsOwned)
	}
	if r.BuildsCompleted != nil {
		add(w.Ledger.BuildsCompleted, *r.BuildsCompleted)
	}
	if r.ItemDelivered != nil && r.ItemDelivered.Delivered != nil {
		add(w.Ledger.DeliveriesByItem[r.ItemDelivered.Item], *r.ItemDelivered.Delivered)
	}

	for _, p := range pairs {
		if target == 0 || float64(p.cur)/float64(p.want) < float64(progress)/float64(target) {
			progress, target = p.cur, p.want
		}
	}
	return progress, target
}

// Unlocks returns the unlock list with status.
func (w *World) Unlocks() []UnlockStatus {
	out := make([]UnlockStatus, 0, len(w.unlocks))
	for _, u := range w.unlocks {
		out = append(out, UnlockStatus{
			ID: u.ID, Title: u.Title, Description: u.Description,
			Earned: u.Earned, Builds: u.GrantsBuild,
		})
	}
	return out
}

// Builds returns the build list with availability.
func (w *World) Builds() []BuildStatus {
	out := make([]BuildStatus, 0, len(w.builds))
	for _, b := range w.builds {
		st := BuildStatus{
			ID: b.ID, Title: b.Title, Description: b.Description, Cost: b.Cost,
			Unlocked:   w.permitted[b.ID],
			Completed:  w.built[b.ID],
			Affordable: w.Ledger.Money >= b.Cost,
		}
		if st.Unlocked && !st.Completed {
			if err := w.City.ValidatePlacement(b); err != nil {
				st.Problem = err.Error()
			} else {
				st.Applicable = true
			}
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuildResult describes what a successful build changed.
type BuildResult struct {
	BuildID   string
	Cost      int
	Revealed  int
	MoneyLeft int
}

// Build purchases and applies an upgrade. It is the player-facing operation
// behind "the map evolves": it validates permission, money, placement and
// atomicity before touching anything.
func (w *World) Build(buildID string) (*BuildResult, error) {
	b, ok := w.buildByID(buildID)
	if !ok {
		return nil, fmt.Errorf("no build %q", buildID)
	}
	if w.built[buildID] {
		return nil, fmt.Errorf("build %q is already completed", buildID)
	}
	if !w.permitted[buildID] {
		return nil, fmt.Errorf("build %q is not unlocked yet", buildID)
	}
	if w.Ledger.Money < b.Cost {
		return nil, fmt.Errorf("build %q costs $%d, you have $%d",
			buildID, b.Cost, w.Ledger.Money)
	}

	revealed, err := w.City.ApplyBuild(b)
	if err != nil {
		return nil, err
	}

	w.Ledger.Money -= b.Cost
	w.Ledger.BuildsCompleted++
	w.built[buildID] = true
	w.applyEconomicEffects(&b)

	w.emit(EventBuild, nil, fmt.Sprintf("built %s (-$%d)", b.Title, b.Cost))
	if revealed > 0 {
		w.emit(EventBuild, nil, fmt.Sprintf("%s revealed %d tiles", b.Title, revealed))
	}
	return &BuildResult{
		BuildID: b.ID, Cost: b.Cost, Revealed: revealed, MoneyLeft: w.Ledger.Money,
	}, nil
}

// applyEconomicEffects handles the non-geographic half of a build's effects:
// money grants, reputation, and unlocking car classes for purchase.
func (w *World) applyEconomicEffects(b *BuildConfig) {
	for _, e := range b.Effects {
		switch e.Type {
		case EffectGrantMoney:
			w.Ledger.Money += e.Amount
			w.Ledger.MoneyEarned += e.Amount
		case EffectGrantRep:
			w.Ledger.Reputation += e.Rep
			w.clampReputation()
		case EffectGrantCarClass:
			if e.Class != "" {
				w.buyableClasses[e.Class] = true
			}
		}
	}
}

// buildByID looks up a build definition.
func (w *World) buildByID(id string) (BuildConfig, bool) {
	for _, b := range w.builds {
		if b.ID == id {
			return b, true
		}
	}
	return BuildConfig{}, false
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

// CarClassInfo is one entry of the purchasable catalogue.
type CarClassInfo struct {
	Class      string
	Price      int
	Capacity   int
	MaxBattery int
	StepCost   int
	Unlocked   bool
	Affordable bool
}

// BuyCarClass reports whether a class may be purchased.
//
// The rule is stated by the map, not inferred: a class is locked only when it
// declares requires_grant and no build has granted it yet. Inferring the lock
// from Price was the previous rule, and it silently produced dead content --
// priced classes that no build could ever unlock.
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

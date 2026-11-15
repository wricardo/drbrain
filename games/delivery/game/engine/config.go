package engine

import "encoding/json"

// CityConfig is the static definition of the city, loaded from city.json.
//
// It is read-only at runtime: a session starts from a CityConfig and then
// applies its own deltas (built upgrades, revealed districts, spawned orders)
// on top. Nothing in a running session mutates the CityConfig it came from,
// which is what makes two sessions on the same map independent.
type CityConfig struct {
	Name       string `json:"name"`
	GridSize   int    `json:"grid_size"`
	TickMS     int    `json:"tick_ms"`
	StartMoney int    `json:"starting_money"`

	// Layout is GridSize rows of GridSize legend characters.
	Layout []string `json:"layout"`

	// Legend maps a layout character to a terrain type name.
	Legend map[string]string `json:"legend"`

	Items     map[string]string `json:"items"`
	Kitchens  []KitchenConfig   `json:"kitchens"`
	Customers []CustomerConfig  `json:"customers"`
	Depots    []DepotConfig     `json:"depots"`
	Chargers  []ChargerConfig   `json:"chargers"`

	InitialCars []InitialCarConfig        `json:"initial_cars"`
	CarClasses  map[string]CarClassConfig `json:"car_classes"`

	Districts    map[string]DistrictConfig `json:"districts"`
	Orders       OrdersConfig              `json:"orders"`
	Charging     ChargingConfig            `json:"charging"`
	Achievements []AchievementConfig       `json:"achievements"`
	Unlocks      []UnlockConfig            `json:"unlocks"`
	Builds       []BuildConfig             `json:"builds"`
}

// KitchenConfig is a restaurant: a pickup point serving a menu of items.
type KitchenConfig struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Pos  Position `json:"pos"`
	Menu []string `json:"menu"`
}

// CustomerConfig is a dropoff point that raises orders for the items it
// demands. OrderIntervalTicks is this customer's own cooldown (gate 1 of
// order spawning); JitterTicks is the +/- randomness applied to it so the
// world does not feel metronomic.
type CustomerConfig struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Pos                Position `json:"pos"`
	Demands            []string `json:"demands"`
	OrderIntervalTicks int      `json:"order_interval_ticks"`
	JitterTicks        int      `json:"jitter_ticks"`
}

// DepotConfig is the home base: cars park and recharge here.
type DepotConfig struct {
	ID          string   `json:"id"`
	Pos         Position `json:"pos"`
	Capacity    int      `json:"capacity"`
	ChargeSlots int      `json:"charge_slots"`
}

// ChargerConfig is a standalone charging point.
type ChargerConfig struct {
	ID       string   `json:"id"`
	Pos      Position `json:"pos"`
	Capacity int      `json:"capacity"`
}

// InitialCarConfig places a car at session start.
type InitialCarConfig struct {
	Class string   `json:"class"`
	Name  string   `json:"name"`
	Pos   Position `json:"pos"`
}

// CarClassConfig is the purchasable archetype. StepCost is the battery spent
// per tile; ground costs more than road, see City.WalkCost.
//
// RequiresGrant makes the unlock rule explicit rather than inferring it from
// Price. Inferring was a trap: a priced class with no grant effect anywhere in
// the city is advertised in the UI and can never be bought. With this field a
// map author states the intent, and the content test can verify every gated
// class actually has a build that grants it.
//
//	RequiresGrant=false  buyable with money alone (the natural second car)
//	RequiresGrant=true   locked until a build grants it
type CarClassConfig struct {
	Speed         int  `json:"speed"`
	Capacity      int  `json:"capacity"`
	MaxBattery    int  `json:"max_battery"`
	StepCost      int  `json:"step_cost"`
	Price         int  `json:"price"`
	RequiresGrant bool `json:"requires_grant,omitempty"`
}

// DistrictConfig marks a named region. Hidden districts render as unexplored
// and are impassable until a build reveals them.
//
// A district is a set of rectangles, each written [x0,y0,x1,y1] inclusive.
// Rectangles are used rather than legend characters because terrain chars are
// already spoken for by the legend, and a district usually spans several kinds
// of terrain (road, ground, chargers) that must be revealed together.
type DistrictConfig struct {
	Description string   `json:"description"`
	Hidden      bool     `json:"hidden"`
	Rects       [][4]int `json:"rects,omitempty"`
}

// OrdersConfig holds every pacing and economy knob for order spawning.
// See IDEA.txt §7.1 for how the three gates use these values.
type OrdersConfig struct {
	MaxActiveOrders int `json:"max_active_orders"`

	// TargetOrdersPerCar is the queue depth the governor aims for per car on
	// the road. It replaced a capacity-based ratio: capacity is measured in
	// boxes, and once a truck holds forty of them a capacity-derived target
	// scales quadratically and stops meaning anything.
	TargetOrdersPerCar float64 `json:"target_orders_per_car"`

	MinSpawnGapTicks        int            `json:"min_spawn_gap_ticks"`
	DeadlineSlack           float64        `json:"deadline_slack"`
	DeadlineSlackByChapter  []float64      `json:"deadline_slack_by_chapter"`
	RewardBase              map[string]int `json:"reward_base"`
	ChapterRewardMultiplier []float64      `json:"chapter_reward_multiplier"`
	DistanceBonusPerTile    int            `json:"distance_bonus_per_tile"`
	ExpirePenaltyReputation float64        `json:"expire_penalty_reputation"`
	LatePayMultiplier       float64        `json:"late_pay_multiplier"`
	ForceFirstOrderAtTick   int            `json:"force_first_order_at_tick"`
	ReputationPerDelivery   float64        `json:"reputation_per_delivery"`
	ReputationPerLate       float64        `json:"reputation_per_late"`

	// Quantity bands: how many boxes a customer can ask for. A band whose
	// Size exceeds every car in the fleet is never rolled, so a big order is
	// only ever raised once the player owns something that can carry it.
	//
	// BandWeight is a relative weight, not a percentage, so the bands need not
	// sum to anything. Chance rises with chapter, which is the escalation: a
	// new player only ever sees singles.
	QuantityBands []QuantityBandConfig `json:"quantity_bands,omitempty"`
}

// QuantityBandConfig is one band of order sizes a customer may ask for.
//
// MinSize and MaxSize are inclusive box counts. FromChapter is the first
// chapter (builds completed) at which the band can be rolled, and Weight is its
// share of the draw relative to the other eligible bands. A band is also
// filtered by the fleet: it only enters the draw when some car can physically
// carry MaxSize, so a heavy band cannot promise work the player cannot do.
type QuantityBandConfig struct {
	MinSize     int `json:"min_size"`
	MaxSize     int `json:"max_size"`
	FromChapter int `json:"from_chapter"`
	Weight      int `json:"weight"`
}

// ChargingConfig is how fast batteries refill, per vehicle charge source.
//
// These are pacing knobs, not physics: at 1/tick a roadside charger is slower
// than a delivery deadline and becomes scenery, while at 10/tick the battery
// stops mattering at all. The sim harness is how the middle gets found.
type ChargingConfig struct {
	DepotRate   int `json:"depot_rate"`   // per tick on a depot tile
	ChargerRate int `json:"charger_rate"` // per tick on a roadside charger
}

// Requirement is a single condition an achievement or unlock checks against
// the session counters. All requirements on a definition must hold (AND).
// The zero value matches nothing, so an unused field is inert.
type Requirement struct {
	Deliveries        *int     `json:"deliveries,omitempty"`
	MoneyEarned       *int     `json:"money_earned,omitempty"`
	MoneyBalance      *int     `json:"money_balance,omitempty"`
	Reputation        *float64 `json:"reputation,omitempty"`
	Tows              *int     `json:"tows,omitempty"`
	ExpiredOrders     *int     `json:"expired_orders,omitempty"`
	OnTimeStreak      *int     `json:"on_time_streak,omitempty"`
	ItemDelivered     *ItemReq `json:"item_delivered,omitempty"`
	CarsOwned         *int     `json:"cars_owned,omitempty"`
	TilesExploredPct  *int     `json:"tiles_explored_pct,omitempty"`
	DistrictsRevealed *int     `json:"districts_revealed,omitempty"`
	BuildsCompleted   *int     `json:"builds_completed,omitempty"`
}

// ItemReq is an item-scoped counter requirement, e.g. 10 pizzas delivered.
type ItemReq struct {
	Item      string `json:"item"`
	Delivered *int   `json:"delivered,omitempty"`
}

// AchievementConfig fires once when its requirement is met. It grants money,
// reputation or cosmetics; it never costs anything and never mutates the map.
//
// Done is runtime state: the world copies these definitions at construction
// and flips Done as they fire, so the config file itself is never mutated.
type AchievementConfig struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Requires    Requirement `json:"requires"`
	GrantMoney  int         `json:"grant_money,omitempty"`
	GrantRep    float64     `json:"grant_reputation,omitempty"`

	Done bool `json:"-"`
}

// UnlockConfig is permission only: meeting it means the listed builds become
// purchasable. It has no cost, because paying is a separate decision.
//
// Earned is runtime state, like AchievementConfig.Done.
type UnlockConfig struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Requires    Requirement `json:"requires"`
	GrantsBuild []string    `json:"grants_build"`

	Earned bool `json:"-"`
}

// BuildConfig is a purchasable map mutation: the player spends Cost and the
// Effects are applied to the grid. Effects are applied in order and the whole
// set is rejected atomically if it would strand anything.
type BuildConfig struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Cost        int           `json:"cost"`
	Effects     []BuildEffect `json:"effects"`
}

// BuildEffect is one atomic change a build makes to the world. The Type
// selects which fields are meaningful; see applyEffects for the vocabulary.
type BuildEffect struct {
	Type string `json:"type"`

	// Tiles is the list of terrain writes for build_bridge / pave_road.
	Tiles []TileWrite `json:"tiles,omitempty"`

	District string `json:"district,omitempty"`

	// Entities is a list of entity definitions added by add_kitchen /
	// add_customer / add_charger / add_depot. They are decoded from the same
	// JSON shapes the map file uses.
	Entities []json.RawMessage `json:"entities,omitempty"`

	// DepotID names the depot an upgrade_depot targets.
	DepotID string `json:"depot,omitempty"`

	Capacity int     `json:"capacity,omitempty"`
	Charge   int     `json:"charge_slots,omitempty"`
	Class    string  `json:"class,omitempty"`
	Amount   int     `json:"amount,omitempty"`
	Rep      float64 `json:"reputation,omitempty"`
}

// TileWrite is one terrain change: put Terrain at (X,Y).
type TileWrite struct {
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Terrain string `json:"terrain"`
}

// Build effect type names.
const (
	EffectBuildBridge    = "build_bridge"
	EffectPaveRoad       = "pave_road"
	EffectRevealDistrict = "reveal_district"
	EffectAddKitchen     = "add_kitchen"
	EffectAddCustomer    = "add_customer"
	EffectAddCharger     = "add_charger"
	EffectAddDepot       = "add_depot"
	EffectUpgradeDepot   = "upgrade_depot"
	EffectGrantCarClass  = "grant_car_class"
	EffectGrantMoney     = "grant_money"
	EffectGrantRep       = "grant_reputation"
)

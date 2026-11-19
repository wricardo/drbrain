package engine

import "encoding/json"

// CityConfig is the static definition of the city, loaded from city.json.
//
// It is read-only at runtime: a session starts from a CityConfig and then
// applies its own deltas (built upgrades, revealed districts, spawned orders)
// on top. Nothing in a running session mutates the CityConfig it came from,
// which is what makes two sessions on the same map independent.
type CityConfig struct {
	// Name is display text only; nothing keys off it.
	Name string `json:"name"`

	// GridSize is the side length of the square grid, in tiles. Must be in
	// [MinGridSize, MaxGridSize]; a mismatch with Layout is a load error.
	GridSize int `json:"grid_size"`

	// TickMS is the wall-clock length of one tick at 1x for live sessions.
	// Zero or negative falls back to DefaultTickMS. The sim harness ignores it
	// and advances a fixed count of ticks.
	TickMS int `json:"tick_ms"`

	// StartMoney is the opening cash balance. Money is not a score: it is
	// spent on cars and builds, while MoneyEarned (lifetime) never decreases.
	StartMoney int `json:"starting_money"`

	// Layout is GridSize rows of GridSize legend characters.
	Layout []string `json:"layout"`

	// Legend maps a layout character to a terrain type name. Keys are single
	// characters; values must be one of the eight terrain names (water,
	// building, road, ground, depot, charger, kitchen, house). A layout
	// character missing from the legend, or a legend value the engine does not
	// know, is a load error.
	Legend map[string]string `json:"legend"`

	// Items maps item id to display label. Kitchens, customers and
	// reward_base reference the id; the label is only used in event text and
	// the UI (a missing label falls back to the id). An id used in a menu or
	// demand but not declared here is a load error.
	Items map[string]string `json:"items"`

	// Kitchens are the pickup points. At least one must serve each item a
	// customer demands, checked at load time.
	Kitchens []KitchenConfig `json:"kitchens"`

	// Customers are the dropoff points that raise orders.
	Customers []CustomerConfig `json:"customers"`

	// Depots are the home bases. At least one is required: cars start and park
	// there, and a tow needs a destination.
	Depots []DepotConfig `json:"depots"`

	// Chargers are standalone charging points, separate from depots.
	Chargers []ChargerConfig `json:"chargers"`

	// InitialCars places cars at session start. Class must name a key in
	// CarClasses (unknown is an error); Name is optional and defaults to
	// "Car N"; Pos must be walkable and must not exceed the tile's capacity.
	InitialCars []InitialCarConfig `json:"initial_cars"`

	// CarClasses maps class id to the purchasable archetype. The ids are what
	// initial_cars and grant_car_class reference.
	CarClasses map[string]CarClassConfig `json:"car_classes"`

	// Districts maps district id to a named region. The id is what the
	// reveal_district build effect targets.
	Districts map[string]DistrictConfig `json:"districts"`

	// Orders is the spawn pacing and economy table.
	Orders OrdersConfig `json:"orders"`

	// Charging is the per-tick refill rate of each charge source.
	Charging ChargingConfig `json:"charging"`

	// Closures is the street-works pacing table. Its zero value means no
	// closures, so a quiet map does not need to opt out.
	Closures ClosureConfig `json:"closures,omitempty"`

	// Fleet is the running-cost table for the vehicles. Its zero value means
	// the fleet is free to keep, which is how the game behaved before upkeep
	// existed; see FleetConfig.
	Fleet FleetConfig `json:"fleet,omitempty"`

	// Achievements are one-shot rewards that fire on their own when their
	// requirement is met.
	Achievements []AchievementConfig `json:"achievements"`

	// Unlocks are permission only: meeting the requirement makes the listed
	// builds purchasable. They never cost or grant money.
	Unlocks []UnlockConfig `json:"unlocks"`

	// Builds are the purchasable map mutations the unlock ladder gates.
	Builds []BuildConfig `json:"builds"`
}

// KitchenConfig is a restaurant: a pickup point serving a menu of items.
type KitchenConfig struct {
	// ID is a map-unique identifier used by orders and event text.
	ID string `json:"id"`

	// Name is display text only.
	Name string `json:"name"`

	// Pos is the pickup tile. It must be on kitchen terrain (the legend
	// character the layout uses for it), checked at load time.
	Pos Position `json:"pos"`

	// Menu lists the item ids this kitchen supplies. Must be non-empty, every
	// entry must be declared in Items, and matching is exact and
	// case-sensitive. A typo is a load error, not a kitchen that silently
	// serves nothing.
	Menu []string `json:"menu"`
}

// CustomerConfig is a dropoff point that raises orders for the items it
// demands. OrderIntervalTicks is this customer's own cooldown (gate 1 of
// order spawning); JitterTicks is the +/- randomness applied to it so the
// world does not feel metronomic.
type CustomerConfig struct {
	// ID is a map-unique identifier used by orders, spawning and save deltas.
	ID string `json:"id"`

	// Name is display text only.
	Name string `json:"name"`

	// Pos is the dropoff tile. It must be on house terrain, checked at load.
	Pos Position `json:"pos"`

	// Demands lists the item ids this customer orders. Must be non-empty,
	// every entry must be declared in Items, and at load time some authored
	// kitchen must already serve it: a demand added by a later build cannot
	// satisfy that check, so the starting map must cover it.
	Demands []string `json:"demands"`

	// OrderIntervalTicks is the cooldown, in ticks, between this customer's
	// orders. Must be positive. This is gate 1 of spawning, evaluated per
	// customer in id order.
	OrderIntervalTicks int `json:"order_interval_ticks"`

	// JitterTicks is the maximum +/- randomness applied to the cooldown so
	// the world does not feel metronomic. The roll is derived from the
	// session seed and tick, so it is reproducible.
	JitterTicks int `json:"jitter_ticks"`
}

// DepotConfig is the home base: cars park and recharge here.
type DepotConfig struct {
	// ID names the depot for tows, buys and upgrade_depot.
	ID string `json:"id"`

	// Pos is the depot tile. It must be on depot terrain, checked at load.
	Pos Position `json:"pos"`

	// Capacity is how many cars may occupy the tile at once: the yard size.
	// It gates BuyCar (a full yard refuses the purchase) and is the tile's
	// movement capacity. Must be positive.
	Capacity int `json:"capacity"`

	// ChargeSlots is how many cars may recharge on this tile per tick. When it
	// is fewer than Capacity the cars that charge are chosen in car id order,
	// deterministically; the others stay parked with no charge that tick.
	// Zero or negative means no slot limit (every car on the tile charges),
	// which is how maps authored before the field existed behave.
	ChargeSlots int `json:"charge_slots"`
}

// ChargerConfig is a standalone charging point.
type ChargerConfig struct {
	// ID is the identifier used by builds and event text.
	ID string `json:"id"`

	// Pos is the charger tile. It must be on charger terrain, checked at load.
	Pos Position `json:"pos"`

	// Capacity is how many cars may occupy the tile at once. Zero or omitted
	// means one car.
	Capacity int `json:"capacity"`
}

// InitialCarConfig places a car at session start.
type InitialCarConfig struct {
	// Class names a key in CarClasses.
	Class string `json:"class"`

	// Name is display text; omitted means "Car N".
	Name string `json:"name"`

	// Pos is the starting tile. It must be walkable and respect the tile's
	// capacity, so a map cannot start in a state the mover would forbid.
	Pos Position `json:"pos"`
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
	// Speed is reserved: the engine currently moves every car exactly one
	// tile per tick regardless of class, so changing it has no effect.
	Speed int `json:"speed"`

	// Capacity is the cargo hold in boxes (not orders). It gates both how
	// much a single order may ask for and how much a car can pick up: a car
	// that cannot fit the whole order takes none of it.
	Capacity int `json:"capacity"`

	// MaxBattery is the full-charge battery in battery units. A new car
	// starts full; the battery drains by StepCost per step and refills per
	// ChargingConfig on a depot or charger tile.
	MaxBattery int `json:"max_battery"`

	// StepCost is the battery spent stepping onto a road tile. Ground costs
	// one extra (see City.WalkCost). Zero or negative is treated as 1.
	StepCost int `json:"step_cost"`

	// Price is the purchase price in dollars. Zero means the class is free,
	// but requires_grant can still lock it behind a build.
	Price int `json:"price"`

	// RequiresGrant, when true, means money alone cannot buy this class: a
	// build with a grant_car_class effect must unlock it first. When false the
	// class is buyable with money alone.
	RequiresGrant bool `json:"requires_grant,omitempty"`

	// UpkeepPerInterval is this class's operating cost per upkeep interval,
	// charged while it is away from a depot. Zero or negative means "derive
	// one from Capacity and the map's fleet.upkeep_per_box_per_interval", NOT
	// "free": there is no per-class way to declare a free class, because
	// hand-tuned rates that disagree with capacity quietly make one class
	// strictly worse to own. Any positive value overrides the derivation.
	UpkeepPerInterval int `json:"upkeep_per_interval,omitempty"`
}

// DistrictConfig marks a named region. Hidden districts render as unexplored
// and are impassable until a build reveals them.
//
// A district is a set of rectangles, each written [x0,y0,x1,y1] inclusive.
// Rectangles are used rather than legend characters because terrain chars are
// already spoken for by the legend, and a district usually spans several kinds
// of terrain (road, ground, chargers) that must be revealed together.
type DistrictConfig struct {
	// Description is display text shown in the UI.
	Description string `json:"description"`

	// Hidden makes every tile in Rects start unexplored and impassable until
	// a build reveals the district. The tiles still exist in Layout; hiding is
	// applied on top of the terrain.
	Hidden bool `json:"hidden"`

	// Rects are inclusive [x0,y0,x1,y1] rectangles. A district with no rects
	// is never revealed by a build and does not count toward the
	// districts_revealed requirement.
	Rects [][4]int `json:"rects,omitempty"`
}

// OrdersConfig holds every pacing and economy knob for order spawning.
// See spawnOrders and the surrounding comments in spawn.go for the gate
// order these values feed.
type OrdersConfig struct {
	// MaxActiveOrders is the hard cap on live orders (pending, assigned and
	// picked up; done and expired do not count). Zero or negative falls back
	// to 6. It also caps the fleet-derived target below.
	MaxActiveOrders int `json:"max_active_orders"`

	// TargetOrdersPerCar is the queue depth the governor aims for per car on
	// the road. It replaced a capacity-based ratio: capacity is measured in
	// boxes, and once a truck holds forty of them a capacity-derived target
	// scales quadratically and stops meaning anything.
	//
	// The target is round(non-stranded cars × ratio), floored at 1 and capped
	// by MaxActiveOrders. Zero or negative falls back to 1.0.
	TargetOrdersPerCar float64 `json:"target_orders_per_car"`

	// MinSpawnGapTicks is the anti-dead-air floor: when nothing is live and
	// no car is carrying anything, at least this many ticks must pass since
	// the last spawn. It is not a gap between ordinary spawns, which are
	// governed by the customers' own cooldowns. Zero falls back to 12.
	MinSpawnGapTicks int `json:"min_spawn_gap_ticks"`

	// DeadlineSlack multiplies the estimated route length to set the deadline.
	// At spawn the estimate is first depot → kitchen → customer; assigning a
	// car re-estimates from the car's position and only ever extends the
	// deadline. It is the fallback used when DeadlineSlackByChapter is empty:
	// a non-empty chapter table replaces this value entirely.
	//
	// An on-time delivery on the deadline tick still counts as on time. Zero
	// falls back to 2.0.
	DeadlineSlack float64 `json:"deadline_slack"`

	// DeadlineSlackByChapter is the per-chapter version of DeadlineSlack,
	// indexed by builds completed. The last entry is reused for later
	// chapters. When set it wins over DeadlineSlack.
	DeadlineSlackByChapter []float64 `json:"deadline_slack_by_chapter"`

	// RewardBase is the base pay per item id. A missing or non-positive entry
	// falls back to 40. It doubles as the expiry fine basis: an expired order
	// costs base/4 in cash, in addition to the reputation penalty.
	RewardBase map[string]int `json:"reward_base"`

	// ChapterRewardMultiplier scales pay by chapter (builds completed),
	// reusing the last entry for later chapters. Empty means 1.0 throughout.
	ChapterRewardMultiplier []float64 `json:"chapter_reward_multiplier"`

	// DistanceBonusPerTile is dollars added to the reward per tile of the
	// kitchen-to-customer route. It is flat per order, not per box, so it only
	// dominates on long hauls.
	DistanceBonusPerTile int `json:"distance_bonus_per_tile"`

	// ExpirePenaltyReputation is reputation lost when an order expires.
	// Reputation is clamped to [0,5].
	ExpirePenaltyReputation float64 `json:"expire_penalty_reputation"`

	// LatePayMultiplier is the fraction of the reward paid for a delivery that
	// lands after the deadline but before the expiry sweep. On-time pay is the
	// full reward. Zero falls back to 0.6.
	LatePayMultiplier float64 `json:"late_pay_multiplier"`

	// ForceFirstOrderAtTick is the tick at or after which one order is raised
	// regardless of every gate, so a player never starts in an empty world.
	// Zero or omitted raises it on the first tick.
	ForceFirstOrderAtTick int `json:"force_first_order_at_tick"`

	// ReputationPerDelivery is reputation gained per on-time delivery. Zero
	// falls back to 0.02.
	ReputationPerDelivery float64 `json:"reputation_per_delivery"`

	// ReputationPerLate is reputation lost per late delivery. Zero falls back
	// to 0.05. Expiry uses ExpirePenaltyReputation instead.
	ReputationPerLate float64 `json:"reputation_per_late"`

	// Quantity bands: how many boxes a customer can ask for. Bands are
	// filtered by chapter, clamped to the fleet's largest hold, and drawn with
	// relative weights. With no bands, or none eligible, every order is a
	// single box.
	QuantityBands []QuantityBandConfig `json:"quantity_bands,omitempty"`
}

// QuantityBandConfig is one band of order sizes a customer may ask for.
//
// MinSize and MaxSize are inclusive box counts. FromChapter is the first
// chapter (builds completed) at which the band can be rolled, and Weight is its
// share of the draw relative to the other eligible bands. The fleet filter
// clamps MaxSize down to the largest hold and drops bands whose MinSize does
// not fit, so a band can never promise work the player cannot carry.
type QuantityBandConfig struct {
	// MinSize is the smallest box count this band can roll. A band whose
	// minimum exceeds the fleet's largest hold is dropped outright, because a
	// clamped band below its own minimum is not that band any more.
	MinSize int `json:"min_size"`

	// MaxSize is the largest box count, inclusive. It is clamped down to the
	// fleet's largest hold rather than dropping the band, so a 30-40 band
	// still raises a 24-box order once a 24-box van exists.
	MaxSize int `json:"max_size"`

	// FromChapter is the first chapter at which this band may roll, where
	// chapter is builds completed, capped at MaxChapter.
	FromChapter int `json:"from_chapter"`

	// Weight is a relative share, not a percentage, so the bands need not sum
	// to anything. Zero or negative is treated as 1.
	Weight int `json:"weight"`
}

// ChargingConfig is how fast batteries refill, per vehicle charge source.
//
// These are pacing knobs, not physics: at 1/tick a roadside charger is slower
// than a delivery deadline and becomes scenery, while at 10/tick the battery
// stops mattering at all. The sim harness is how the middle gets found.
type ChargingConfig struct {
	// DepotRate is battery restored per tick on a depot tile, up to the car's
	// max_battery. Zero or negative falls back to 6. Must live in the config
	// because pacing at 1/tick makes a depot slower than a deadline.
	DepotRate int `json:"depot_rate"`

	// ChargerRate is battery restored per tick on a standalone charger tile.
	// Zero or negative falls back to 2. It does not apply to road or kitchen
	// tiles: the tick loop gates recharging on the tile being a depot or
	// charger first.
	ChargerRate int `json:"charger_rate"`
}

// FleetConfig is the running cost of owning more vehicles than you drive.
//
// The cost applies to cars that are NOT parked on a depot tile. That is a
// deliberate choice over "idle cars cost money": the engine has no sell or
// scrap action, so a per-tick charge on an unsellable asset is an unrecoverable
// mistake for the player, with no exit. Parking is the exit, and it needs no new
// verb — it also gives DepotConfig.Capacity a meaning beyond gating purchases.
//
// The whole block is optional and its zero value DISABLES upkeep (UpkeepInterval
// defaults to 0, and a zero interval charges nothing). A map that omits `fleet`
// therefore behaves exactly as it did before this feature existed, which is what
// keeps the shipped map and every existing save on the old economy until someone
// tunes it on purpose.
//
// Rates are charged every UpkeepIntervalTicks rather than every tick on purpose.
// Gross income on the shipped map is roughly $3.50 per car-tick, so an
// integer-dollars-per-tick charge has no usable setting: $1/tick is ~29% of one
// car's revenue and $0 is nothing at all. A per-interval charge recovers the
// tuning room the integer floor throws away. There is no code default for the
// interval: the shipped map uses 120, and zero disables upkeep.
type FleetConfig struct {
	// UpkeepIntervalTicks is how many ticks pass between upkeep charges.
	// Zero means upkeep is off entirely; negative is rejected by the loader.
	UpkeepIntervalTicks int `json:"upkeep_interval_ticks,omitempty"`

	// DefaultUpkeepPerInterval is the charge only for a class that has no
	// capacity to derive one from (Capacity <= 0) and declares no
	// upkeep_per_interval of its own. It does not override the derivation.
	DefaultUpkeepPerInterval int `json:"default_upkeep_per_interval,omitempty"`

	// UpkeepPerBoxPerInterval scales the capacity-derived rate: a class's charge
	// is Capacity*UpkeepPerBoxPerInterval/4 in integer arithmetic, floored at
	// 1. (The divisor is what makes "per box" mean a group of four boxes; with
	// the shipped values a capacity-4 scooter pays 4*1/4 = $1 and a capacity-40
	// truck pays 40*1/4 = $10 per interval.) Scaling by capacity rather than
	// hand-tuning each class keeps the rate proportional to the revenue
	// headroom the class actually has, so a new class cannot be added with a
	// rate that contradicts its own hold size.
	//
	// WARNING: this knob has a measured collapse threshold, not a smooth curve.
	// See the long comment in fleet.go before raising it.
	UpkeepPerBoxPerInterval int `json:"upkeep_per_box_per_interval,omitempty"`

	// TowFeePerTile is dollars per tile of route distance between the car and
	// the depot it is towed to. Zero (with a zero minimum) makes towing free.
	TowFeePerTile int `json:"tow_fee_per_tile,omitempty"`

	// TowFeeMin floors the tow fee, so a nudge from the next block is not free
	// and the move-one-tile-per-tow exploit is never cheaper than driving.
	TowFeeMin int `json:"tow_fee_min,omitempty"`
}

// Requirement is a single condition an achievement or unlock checks against
// the session counters. All requirements on a definition must hold (AND).
// The zero value matches nothing, so an unused field is inert.
type Requirement struct {
	// Deliveries is the least number of ON-TIME deliveries. Late deliveries
	// increment late_deliveries, not this counter.
	Deliveries *int `json:"deliveries,omitempty"`

	// MoneyEarned is the least lifetime gross earnings. It never decreases:
	// delivery pay, achievement grants and build grants add to it; upkeep,
	// tow fees and expiry fines do not subtract from it. Use MoneyBalance for
	// cash on hand.
	MoneyEarned *int `json:"money_earned,omitempty"`

	// MoneyBalance is the least current cash balance.
	MoneyBalance *int `json:"money_balance,omitempty"`

	// Reputation is the least current reputation, against a 0..5 scale.
	Reputation *float64 `json:"reputation,omitempty"`

	// Tows is the least number of tows performed.
	Tows *int `json:"tows,omitempty"`

	// ExpiredOrders is the least number of orders that expired.
	ExpiredOrders *int `json:"expired_orders,omitempty"`

	// OnTimeStreak is the least current consecutive on-time delivery streak.
	// A late delivery or an expiry resets it to zero.
	OnTimeStreak *int `json:"on_time_streak,omitempty"`

	// ItemDelivered is a per-item delivery requirement. A nil Delivered
	// matches vacuously true.
	ItemDelivered *ItemReq `json:"item_delivered,omitempty"`

	// CarsOwned is the least fleet size, counting stranded cars.
	CarsOwned *int `json:"cars_owned,omitempty"`

	// TilesExploredPct is the least percentage of ALL tiles revealed,
	// including impassable water and buildings. Integer division.
	TilesExploredPct *int `json:"tiles_explored_pct,omitempty"`

	// DistrictsRevealed is the least number of authored districts that are no
	// longer hidden. A district counts once at least one of its tiles is
	// visible; districts with no rects never count.
	DistrictsRevealed *int `json:"districts_revealed,omitempty"`

	// BuildsCompleted is the least number of distinct builds completed. It is
	// also the chapter index the pacing tables use, capped at MaxChapter.
	BuildsCompleted *int `json:"builds_completed,omitempty"`
}

// ItemReq is an item-scoped counter requirement, e.g. 10 pizzas delivered.
type ItemReq struct {
	// Item is the item id, as declared in Items.
	Item string `json:"item"`

	// Delivered is the least number of that item delivered. Late deliveries
	// count here, unlike the global Deliveries counter. Nil matches anything.
	Delivered *int `json:"delivered,omitempty"`
}

// AchievementConfig fires once when its requirement is met. It grants money
// or reputation; it never costs anything and never mutates the map.
//
// Done is runtime state: the world copies these definitions at construction
// and flips Done as they fire, so the config file itself is never mutated.
type AchievementConfig struct {
	// ID is a stable identifier; progress and saves key off it.
	ID string `json:"id"`

	// Title is display text.
	Title string `json:"title"`

	// Description is display text.
	Description string `json:"description"`

	// Requires is evaluated at the end of every tick until it holds. All
	// populated fields must hold (AND).
	Requires Requirement `json:"requires"`

	// GrantMoney is paid once when the achievement fires. It adds to both the
	// cash balance and lifetime MoneyEarned.
	GrantMoney int `json:"grant_money,omitempty"`

	// GrantRep is reputation granted once when the achievement fires, clamped
	// to [0,5].
	GrantRep float64 `json:"grant_reputation,omitempty"`

	// Done is per-session runtime state, never read from or written to JSON.
	Done bool `json:"-"`
}

// UnlockConfig is permission only: meeting it means the listed builds become
// purchasable. It has no cost, because paying is a separate decision.
//
// Earned is runtime state, never serialised: on load it is re-derived from the
// session's permitted-build set, so a save does not store unlock ids.
type UnlockConfig struct {
	// ID is a stable identifier used by the API and event text.
	ID string `json:"id"`

	// Title is display text.
	Title string `json:"title"`

	// Description is display text.
	Description string `json:"description"`

	// Requires is evaluated at the end of every tick until it holds.
	Requires Requirement `json:"requires"`

	// GrantsBuild lists the build ids (matching BuildConfig.ID) that become
	// purchasable when this unlock is earned.
	GrantsBuild []string `json:"grants_build"`

	// Earned is per-session runtime state, never read from or written to JSON.
	Earned bool `json:"-"`
}

// BuildConfig is a purchasable map mutation: the player spends Cost and the
// Effects are applied to the grid. Effects are applied in order and the whole
// set is rejected atomically if it would strand anything.
//
// A build is applicable at most once per session, needs its id to be granted by
// an unlock, and must be affordable in cash at the moment it is bought.
type BuildConfig struct {
	// ID is a stable identifier, referenced by UnlockConfig.GrantsBuild and
	// recorded in saves once completed.
	ID string `json:"id"`

	// Title is display text.
	Title string `json:"title"`

	// Description is display text.
	Description string `json:"description"`

	// Cost is the price in dollars, paid from the cash balance.
	Cost int `json:"cost"`

	// Effects are applied in order, and atomically with the rest of the build:
	// if any effect fails or would strand a kitchen or customer, none land.
	Effects []BuildEffect `json:"effects"`
}

// BuildEffect is one atomic change a build makes to the world. The Type
// selects which fields are meaningful; see applyEffects for the vocabulary.
//
//	build_bridge, pave_road   Tiles
//	reveal_district           District
//	add_kitchen               Entities (KitchenConfig shape)
//	add_customer              Entities (CustomerConfig shape)
//	add_charger               Entities (ChargerConfig shape)
//	add_depot                 Entities (DepotConfig shape)
//	upgrade_depot             DepotID, Capacity, Charge
//	grant_car_class           Class
//	grant_money               Amount
//	grant_reputation          Rep
//
// The geographic effects mutate the City. The three grant effects are no-ops
// there and are applied by World.Build after the city effects succeed.
type BuildEffect struct {
	// Type is one of the effect names listed above. Unknown types fail the
	// whole build.
	Type string `json:"type"`

	// Tiles is the list of terrain writes for build_bridge / pave_road. Each
	// written tile is also revealed.
	Tiles []TileWrite `json:"tiles,omitempty"`

	// District names the district revealed by reveal_district.
	District string `json:"district,omitempty"`

	// Entities is a list of entity definitions added by add_kitchen /
	// add_customer / add_charger / add_depot. They are decoded from the same
	// JSON shapes the map file uses.
	Entities []json.RawMessage `json:"entities,omitempty"`

	// DepotID names the depot an upgrade_depot targets.
	DepotID string `json:"depot,omitempty"`

	// Capacity replaces the target depot's yard size for upgrade_depot. Zero
	// or negative leaves it unchanged.
	Capacity int `json:"capacity,omitempty"`

	// Charge replaces the target depot's charge slots for upgrade_depot. Zero
	// or negative leaves them unchanged.
	Charge int `json:"charge_slots,omitempty"`

	// Class is the car class made buyable by grant_car_class. It must name a
	// key in CarClasses.
	Class string `json:"class,omitempty"`

	// Amount is dollars granted by grant_money. It adds to both cash and
	// lifetime MoneyEarned.
	Amount int `json:"amount,omitempty"`

	// Rep is reputation granted by grant_reputation, clamped to [0,5].
	Rep float64 `json:"reputation,omitempty"`
}

// TileWrite is one terrain change: put Terrain at (X,Y).
type TileWrite struct {
	// X is the column, east positive.
	X int `json:"x"`

	// Y is the row, south positive.
	Y int `json:"y"`

	// Terrain is one of the eight terrain names (water, building, road,
	// ground, depot, charger, kitchen, house). An unknown name fails the
	// build.
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

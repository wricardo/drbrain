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
	// Suggested: "DrBrain City".
	Name string `json:"name"`

	// GridSize is the side length of the square grid, in tiles. Must be in
	// [MinGridSize, MaxGridSize]; a mismatch with Layout is a load error.
	// Suggested: 22.
	GridSize int `json:"grid_size"`

	// TickMS is the wall-clock length of one tick at 1x for live sessions.
	// Required: positive. Suggested: 600. The sim harness ignores it and
	// advances a fixed count of ticks.
	TickMS int `json:"tick_ms"`

	// GroundExtraStepCost is the extra battery spent entering a ground tile,
	// on top of the car class's step_cost. Required: non-negative.
	// Suggested: 1. Zero makes ground cost the same as road.
	GroundExtraStepCost int `json:"ground_extra_step_cost"`

	// TicksPerMonth is the accounting month length for the monthly
	// income/expense report. Required: positive. Suggested: 600.
	TicksPerMonth int `json:"ticks_per_month"`

	// MaxEvents bounds the retained event feed. Required: positive.
	// Suggested: 500.
	MaxEvents int `json:"max_events"`

	// MaxReputation is the top of the reputation scale. Required: positive.
	// Suggested: 5.0.
	MaxReputation float64 `json:"max_reputation"`

	// MaxChapter caps the chapter index used by tuning tables. Chapters count
	// applied growth stages. Required: positive. Suggested: 5.
	MaxChapter int `json:"max_chapter"`

	// AutosaveTicks is how many world ticks may pass between background
	// saves. Required: positive. Suggested: 60.
	AutosaveTicks int `json:"autosave_ticks"`

	// StartMoney is the opening cash balance. Money is not a score: it is
	// spent on cars, charging, tows and upkeep, while MoneyEarned (lifetime)
	// never decreases. Suggested: 0.
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
	// reveal_district growth effect targets.
	Districts map[string]DistrictConfig `json:"districts"`

	// Orders is the spawn pacing and economy table.
	Orders OrdersConfig `json:"orders"`

	// Charging is the per-tick refill rate of each charge source.
	Charging ChargingConfig `json:"charging"`

	// Closures is the street-works pacing table. The block is required so a
	// map states whether closures are on; set enabled=false for a quiet map.
	Closures *ClosureConfig `json:"closures"`

	// Fleet is the running-cost table for the vehicles. The block is required
	// so a map states its upkeep choice; set upkeep_interval_ticks=0 to
	// disable upkeep explicitly.
	Fleet *FleetConfig `json:"fleet"`

	// Growth is the city's scheduled expansion. Each stage fires once, on its
	// own tick, and applies its effects atomically: districts open, roads and
	// bridges appear, chargers and depots are added, car classes are licensed.
	// Nothing here is purchased; the player's economy is spent on cars and
	// running the fleet, while the city grows on a fixed timeline.
	Growth []GrowthConfig `json:"growth"`
}

// KitchenConfig is a restaurant: a pickup point serving a menu of items.
type KitchenConfig struct {
	// ID is a map-unique identifier used by orders and event text.
	ID string `json:"id"`

	// Name is display text only. Suggested: a readable place name.
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

	// Name is display text only. Suggested: a readable place name.
	Name string `json:"name"`

	// Pos is the dropoff tile. It must be on house terrain, checked at load.
	Pos Position `json:"pos"`

	// Demands lists the item ids this customer orders. Must be non-empty,
	// every entry must be declared in Items, and at load time some authored
	// kitchen must already serve it: a demand added by a later growth stage
	// cannot satisfy that check, so the starting map must cover it.
	Demands []string `json:"demands"`

	// OrderIntervalTicks is the cooldown, in ticks, between this customer's
	// orders. Must be positive. This is gate 1 of spawning, evaluated per
	// customer in id order. Suggested: 40.
	OrderIntervalTicks int `json:"order_interval_ticks"`

	// JitterTicks is the maximum +/- randomness applied to the cooldown so
	// the world does not feel metronomic. The roll is derived from the
	// session seed and tick, so it is reproducible. Suggested: 10.
	JitterTicks int `json:"jitter_ticks"`
}

// DepotConfig is the home base: cars park and recharge here.
type DepotConfig struct {
	// ID names the depot for tows, buys and upgrade_depot.
	ID string `json:"id"`

	// Pos is the depot tile. It must be on depot terrain, checked at load.
	Pos Position `json:"pos"`

	// ChargeSlots is how many cars may recharge on this tile per tick. When
	// positive, the cars that charge are chosen in car id order,
	// deterministically; the others stay parked with no charge that tick.
	// Zero or negative means no slot limit (every car on the tile charges).
	// Suggested: 4.
	ChargeSlots int `json:"charge_slots"`
}

// ChargerConfig is a standalone charging point.
type ChargerConfig struct {
	// ID is the identifier used by growth effects (add_charger), ledger memos
	// and event text.
	ID string `json:"id"`

	// Pos is the charger tile. It must be on charger terrain, checked at load.
	Pos Position `json:"pos"`

	// Capacity is how many cars may occupy the tile at once. Required:
	// positive. Suggested: 1.
	Capacity int `json:"capacity"`

	// Rate is battery restored per tick, overriding charging.charger_rate.
	// Zero inherits the city's rate; a rate above the depot's makes this a
	// supercharger, so pair it with a higher price. Suggested: 0 (inherit).
	Rate int `json:"rate"`

	// PricePerTick is the default price in dollars for one full charging tick
	// at this charger, overriding charging.charger_price_per_tick. nil
	// inherits the city default; an explicit 0 makes this charger free. The
	// last tick of a session is prorated by the energy actually delivered, so
	// a partial tick costs no more than a full one. Suggested: nil (inherit).
	PricePerTick *int `json:"price_per_tick"`
}

// InitialCarConfig places a car at session start.
type InitialCarConfig struct {
	// Class names a key in CarClasses.
	Class string `json:"class"`

	// Name is display text; omitted means "Car N".
	// Suggested: a readable name such as "Scooter One".
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
// class actually has a growth stage that grants it.
//
//	RequiresGrant=false  buyable with money alone (every shipped class)
//	RequiresGrant=true   locked until a growth stage grants it
type CarClassConfig struct {
	// Capacity is the cargo hold in boxes (not orders). Required: positive.
	// It gates both how much a single order may ask for and how much a car
	// can pick up: a car that cannot fit the whole order takes none of it.
	// Suggested: 4 (scooter), 24 (van), 40 (truck).
	Capacity int `json:"capacity"`

	// MaxBattery is the full-charge battery in battery units. Required:
	// positive. A new car starts full; the battery drains by StepCost per step
	// and refills per ChargingConfig on a depot or charger tile.
	// Suggested: 24 (scooter), 36 (van), 48 (truck).
	MaxBattery int `json:"max_battery"`

	// StepCost is the battery spent stepping onto a road tile. Ground costs
	// GroundExtraStepCost more (see City.WalkCost). Required: positive.
	// Suggested: 1.
	StepCost int `json:"step_cost"`

	// Price is the purchase price in dollars. Required: non-negative. Zero
	// means the class is free, but requires_grant can still lock it behind a
	// growth stage. Suggested: 0 (scooter), 600 (van), 2100 (truck).
	Price int `json:"price"`

	// RequiresGrant, when true, means money alone cannot buy this class: a
	// growth stage with a grant_car_class effect must unlock it first. When
	// false the class is buyable with money alone. Suggested: false.
	RequiresGrant bool `json:"requires_grant,omitempty"`

	// UpkeepPerInterval is this class's operating cost per upkeep interval,
	// charged while it is away from a depot. Zero or negative means "derive
	// one from Capacity and the map's fleet.upkeep_per_box_per_interval", NOT
	// "free": there is no per-class way to declare a free class, because
	// hand-tuned rates that disagree with capacity quietly make one class
	// strictly worse to own. Any positive value overrides the derivation.
	// Suggested: 0 (derive).
	UpkeepPerInterval int `json:"upkeep_per_interval"`
}

// DistrictConfig marks a named region. Hidden districts render as unexplored
// and are impassable until a growth stage reveals them.
//
// A district is a set of rectangles, each written [x0,y0,x1,y1] inclusive.
// Rectangles are used rather than legend characters because terrain chars are
// already spoken for by the legend, and a district usually spans several kinds
// of terrain (road, ground, chargers) that must be revealed together.
type DistrictConfig struct {
	// Description is display text shown in the UI.
	Description string `json:"description"`

	// Hidden makes every tile in Rects start unexplored and impassable until
	// a growth stage reveals the district. The tiles still exist in Layout;
	// hiding is applied on top of the terrain.
	Hidden bool `json:"hidden"`

	// Rects are inclusive [x0,y0,x1,y1] rectangles. A district with no rects
	// has no tiles and reveals nothing.
	Rects [][4]int `json:"rects,omitempty"`
}

// OrdersConfig holds every pacing and economy knob for order spawning.
// See spawnOrders and the surrounding comments in spawn.go for the gate
// order these values feed.
type OrdersConfig struct {
	// MaxActiveOrders is the hard cap on live orders (pending, assigned and
	// picked up; done and expired do not count). Required: positive.
	// Suggested: 6. It also caps the population-derived target below.
	MaxActiveOrders int `json:"max_active_orders"`

	// TargetOrdersPerCustomer is the queue depth the governor aims for per
	// visible customer. Demand is anchored to the map's population, not to the
	// player's fleet: buying cars must not create more work, or the queue
	// treadmill makes fleet growth unable to catch up. Visible customers are
	// the ones whose district is revealed; a hidden district contributes
	// nothing until a growth stage opens it.
	//
	// The target is round(visible customers × ratio), floored at 1 and capped
	// by MaxActiveOrders. Required: positive. Suggested: 0.75.
	TargetOrdersPerCustomer float64 `json:"target_orders_per_customer"`

	// MinSpawnGapTicks is the anti-dead-air floor: when nothing is live and
	// no car is carrying anything, at least this many ticks must pass since
	// the last spawn. It is not a gap between ordinary spawns, which are
	// governed by the customers' own cooldowns. Required: positive.
	// Suggested: 12.
	MinSpawnGapTicks int `json:"min_spawn_gap_ticks"`

	// DeadlineSlack multiplies the estimated route length to set the deadline.
	// At spawn the estimate is first depot → kitchen → customer; assigning a
	// car re-estimates from the car's position and only ever extends the
	// deadline. It is the fallback used when DeadlineSlackByChapter is empty:
	// a non-empty chapter table replaces this value entirely.
	//
	// An on-time delivery on the deadline tick still counts as on time.
	// Required: positive. Suggested: 2.0.
	DeadlineSlack float64 `json:"deadline_slack"`

	// DeadlineSlackByChapter is the per-chapter version of DeadlineSlack,
	// indexed by growth stages applied. The last entry is reused for later
	// chapters. When set it wins over DeadlineSlack. Every entry must be
	// positive.
	DeadlineSlackByChapter []float64 `json:"deadline_slack_by_chapter"`

	// RewardBase is the base pay per item id. Every item declared in Items
	// must have a positive entry; there is no fallback. It doubles as the
	// expiry fine basis: an expired order costs base/ExpireFineDivisor in
	// cash, in addition to the reputation penalty. Suggested: 40 per item.
	RewardBase map[string]int `json:"reward_base"`

	// RewardBulkBase and RewardBulkPerBox shape the affine quantity
	// multiplier: pay = base × chapter × (RewardBulkBase +
	// RewardBulkPerBox × quantity). The affine shape keeps a full truck a good
	// job without making bulk strictly better than small runs.
	//
	// Both are required: positive. Suggested: 0.6 and 0.4.
	RewardBulkBase   float64 `json:"reward_bulk_base"`
	RewardBulkPerBox float64 `json:"reward_bulk_per_box"`

	// ExpireFineDivisor divides the item's RewardBase to get the cash fine
	// for an expired order. Required: positive. Suggested: 4.
	ExpireFineDivisor int `json:"expire_fine_divisor"`

	// MinRouteTicks and MinDeadlineTicks floor the route estimate and the
	// resulting deadline: the route estimate is raised to MinRouteTicks before
	// slack is applied, and the deadline is raised to MinDeadlineTicks.
	// Required: positive. Suggested: 5 and 10.
	MinRouteTicks    int64 `json:"min_route_ticks"`
	MinDeadlineTicks int64 `json:"min_deadline_ticks"`

	// UnreachableLegTicks is substituted for a leg with no route when
	// estimating a deadline. Required: positive. Suggested: 20.
	UnreachableLegTicks int64 `json:"unreachable_leg_ticks"`

	// ChapterRewardMultiplier scales pay by chapter (growth stages applied),
	// reusing the last entry for later chapters. Empty means 1.0 throughout.
	// Every entry must be positive. Suggested: 1.0 for one chapter.
	ChapterRewardMultiplier []float64 `json:"chapter_reward_multiplier"`

	// DistanceBonusPerTile is dollars added to the reward per tile of the
	// kitchen-to-customer route. It is flat per order, not per box, so it only
	// dominates on long hauls. Required: non-negative. Suggested: 3.
	DistanceBonusPerTile int `json:"distance_bonus_per_tile"`

	// ExpirePenaltyReputation is the MAXIMUM reputation lost when an order
	// expires: it applies at standing 1.0 and above, and scales down linearly
	// below that (see World.ExpirePenalty), so a collapsed fleet can climb back.
	// Reputation is clamped to [0, max_reputation]. Required: non-negative.
	// Suggested: 0.10.
	ExpirePenaltyReputation float64 `json:"expire_penalty_reputation"`

	// LatePayMultiplier is the fraction of the reward paid for a delivery that
	// lands after the deadline but before the expiry sweep. On-time pay is the
	// full reward. Required: > 0 and <= 1. Suggested: 0.6.
	LatePayMultiplier float64 `json:"late_pay_multiplier"`

	// ForceFirstOrderAtTick is the tick at or after which one order is raised
	// regardless of every gate, so a player never starts in an empty world.
	// Zero raises it on the first tick; there is no fallback. Suggested: 0.
	ForceFirstOrderAtTick int `json:"force_first_order_at_tick"`

	// ReputationPerDelivery is reputation gained per on-time delivery.
	// Required: non-negative. Suggested: 0.02.
	ReputationPerDelivery float64 `json:"reputation_per_delivery"`

	// ReputationPerLate is reputation lost per late delivery. Expiry uses
	// ExpirePenaltyReputation instead. Required: non-negative.
	// Suggested: 0.05.
	ReputationPerLate float64 `json:"reputation_per_late"`

	// RampTicks is the session tick at which demand scaling reaches 100%.
	// Quantity bands and customer cooldowns are percentages of this ramp, not
	// chapter steps. Required: positive. Suggested: the last growth-stage
	// tick, or 2400 when the map has no growth.
	RampTicks int64 `json:"ramp_ticks"`

	// CustomerCooldownMinScale is the fraction of a customer's base cooldown
	// that remains at 100% ramp. Required: > 0 and <= 1. Suggested: 0.5:
	// orders arrive twice as often at the end of the ramp as at the start.
	CustomerCooldownMinScale float64 `json:"customer_cooldown_min_scale"`

	// Quantity bands: how many boxes a customer can ask for. Bands are
	// filtered by ramp progress and drawn with relative weights. With no bands,
	// or none eligible, every order is a single box. A band may exceed the
	// current fleet's largest hold on purpose: the pending oversized order is
	// the demand signal to upgrade a vehicle.
	QuantityBands []QuantityBandConfig `json:"quantity_bands,omitempty"`
}

// QuantityBandConfig is one band of order sizes a customer may ask for.
//
// MinSize and MaxSize are inclusive box counts. FromProgress is the ramp
// fraction (0..1) at which the band reaches its full Weight; EarlyWeight is the
// share it keeps before that point. Weight is its share relative to the other
// eligible bands.
//
// Bands are NOT filtered by the player's current fleet. A map should be able
// to offer a thirteen-box job before the player owns a van: the unassignable
// order is the demand signal that makes upgrading a vehicle a decision rather
// than an optional luxury. The assign check still refuses to put more boxes in
// a car than its hold allows, so an oversized order can never corrupt the
// cargo invariant.
//
// EarlyWeight is what keeps every vehicle class in the game at every stage: a
// band with a positive EarlyWeight stays in the draw from tick 0 at a small
// share, so a late-game city still asks for scooter- and van-sized hauls, and
// an early city can occasionally ask for a bigger one. Zero (the default)
// keeps the original hard gate: the band does not exist until FromProgress.
type QuantityBandConfig struct {
	// MinSize is the smallest box count this band can roll.
	MinSize int `json:"min_size"`

	// MaxSize is the largest box count, inclusive.
	MaxSize int `json:"max_size"`

	// FromProgress is the ramp fraction (0..1) at which this band starts using
	// its full Weight. It is progress through RampTicks, not a chapter count.
	FromProgress float64 `json:"from_progress,omitempty"`

	// EarlyWeight is this band's relative share before FromProgress. Zero keeps
	// the band out of the draw until FromProgress; a positive value makes the
	// band available from tick 0 at that reduced weight. Required:
	// non-negative. Suggested: 1-3 for bands that should always exist.
	EarlyWeight int `json:"early_weight,omitempty"`

	// Weight is a relative share, not a percentage, so the bands need not sum
	// to anything. Required: positive. Suggested: 30-40 per band.
	Weight int `json:"weight"`
}

// ChargingConfig is how fast batteries refill, per vehicle charge source.
//
// These are pacing knobs, not physics: at 1/tick a roadside charger is slower
// than a delivery deadline and becomes scenery, while at 10/tick the battery
// stops mattering at all. The sim harness is how the middle gets found.
type ChargingConfig struct {
	// DepotRate is battery restored per tick on a depot tile, up to the car's
	// max_battery. Required: positive. Suggested: 6. Must live in the config
	// because pacing at 1/tick makes a depot slower than a deadline.
	DepotRate int `json:"depot_rate"`

	// ChargerRate is battery restored per tick on a standalone charger tile.
	// Required: positive. Suggested: 2. It does not apply to road or kitchen
	// tiles: the tick loop gates recharging on the tile being a depot or
	// charger first.
	ChargerRate int `json:"charger_rate"`

	// ChargerPricePerTick is the default price in dollars for one full tick
	// at a standalone charger. Required: non-negative. Zero means free;
	// suggested: 1. A charger may override it with its own price_per_tick.
	// Depots are always free: they are the home base, not a service station.
	ChargerPricePerTick int `json:"charger_price_per_tick"`
}

// FleetConfig is the running cost of owning more vehicles than you drive.
//
// The cost applies to cars that are NOT parked on a depot tile. That is a
// deliberate choice over "idle cars cost money": the engine has no sell or
// scrap action, so a per-tick charge on an unsellable asset is an unrecoverable
// mistake for the player, with no exit. Parking is the exit, and it needs no new
// verb: depots accept any number of cars, so a car is always somewhere to park.
//
// The block is required (CityConfig.Fleet is a pointer) so a map cannot omit
// its upkeep choice by accident. Set UpkeepIntervalTicks to 0 to disable upkeep
// explicitly; any positive interval charges once per that many ticks.
//
// Rates are charged every UpkeepIntervalTicks rather than every tick on purpose.
// Gross income on the shipped map is roughly $3.50 per car-tick, so an
// integer-dollars-per-tick charge has no usable setting: $1/tick is ~29% of one
// car's revenue and $0 is nothing at all. A per-interval charge recovers the
// tuning room the integer floor throws away. Suggested: 120.
type FleetConfig struct {
	// UpkeepIntervalTicks is how many ticks pass between upkeep charges.
	// Required field: 0 disables upkeep, a negative value is rejected.
	// Suggested: 120.
	UpkeepIntervalTicks int `json:"upkeep_interval_ticks"`

	// DefaultUpkeepPerInterval is the charge only for a class that has no
	// capacity to derive one from (Capacity <= 0) and declares no
	// upkeep_per_interval of its own. It does not override the derivation.
	// Required: non-negative. Suggested: 1.
	DefaultUpkeepPerInterval int `json:"default_upkeep_per_interval"`

	// UpkeepBoxDivisor is the number of cargo boxes per unit of the
	// capacity-derived rate: a class's charge is
	// Capacity*UpkeepPerBoxPerInterval/UpkeepBoxDivisor in integer
	// arithmetic, floored at 1. Required: positive. Suggested: 4, which makes
	// a capacity-4 scooter pay $1 and a capacity-40 truck pay $10 per interval
	// with UpkeepPerBoxPerInterval=1.
	UpkeepBoxDivisor int `json:"upkeep_box_divisor"`

	// UpkeepPerBoxPerInterval scales the capacity-derived rate. Scaling by
	// capacity rather than hand-tuning each class keeps the rate proportional
	// to the revenue headroom the class actually has, so a new class cannot be
	// added with a rate that contradicts its own hold size. Required:
	// non-negative. Suggested: 1.
	//
	// WARNING: this knob has a measured collapse threshold, not a smooth curve.
	// See the long comment in fleet.go before raising it.
	UpkeepPerBoxPerInterval int `json:"upkeep_per_box_per_interval"`

	// TowFeePerTile is dollars per tile of route distance between the car and
	// the depot it is towed to. Required: non-negative. Suggested: 2. Zero
	// (with a zero minimum) makes towing free.
	TowFeePerTile int `json:"tow_fee_per_tile"`

	// TowFeeMin floors the tow fee, so a nudge from the next block is not free
	// and the move-one-tile-per-tow exploit is never cheaper than driving.
	// Required: non-negative. Suggested: 25.
	TowFeeMin int `json:"tow_fee_min"`
}

// GrowthConfig is one scheduled expansion of the city. Stages fire in file
// order as the clock reaches Tick; each applies at most once per session.
//
// Tick is absolute session time (0 is session start), so a 240-tick stage is
// four minutes into a 1x game and one minute at 4x. The schedule is content,
// not a rule: a map can expand fast and hard or slowly, and a replay of the
// same seed always sees the same city at the same tick.
type GrowthConfig struct {
	// ID is a stable identifier used by saves, events and the API.
	ID string `json:"id"`

	// Tick is the session tick at or after which this stage applies.
	Tick int64 `json:"tick"`

	// Title is display text for the growth timeline.
	Title string `json:"title"`

	// Description is display text: what the city gains at this stage.
	Description string `json:"description"`

	// Effects are applied in order and atomically: if any effect fails, or
	// would strand a kitchen or customer, none of them land.
	Effects []GrowthEffect `json:"effects"`
}

// GrowthEffect is one atomic change a growth stage makes to the world. The Type
// selects which fields are meaningful; see applyEffect for the vocabulary.
//
//	build_bridge, pave_road   Tiles
//	reveal_district           District
//	add_kitchen               Entities (KitchenConfig shape)
//	add_customer              Entities (CustomerConfig shape)
//	add_charger               Entities (ChargerConfig shape)
//	add_depot                 Entities (DepotConfig shape)
//	upgrade_depot             DepotID, Charge
//	grant_car_class           Class
//	grant_money               Amount
//	grant_reputation          Rep
//
// The geographic effects mutate the City. The three grant effects are no-ops
// there and are applied by World.applyGrowth after the city effects succeed.
type GrowthEffect struct {
	// Type is one of the effect names listed above. Unknown types fail the
	// whole stage.
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
	// growth stage.
	Terrain string `json:"terrain"`
}

// Growth effect type names.
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

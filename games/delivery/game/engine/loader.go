package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadCityConfig reads and decodes a city JSON file. Unknown fields are an
// error: a typo in a knob name must not silently fall back to a zero value,
// because most of these knobs are pacing multipliers where zero is harmful.
//
// The cost of that strictness is that a map file cannot carry prose. JSON has no
// comments, and a "_comment" key is an unknown field like any other, so adding
// one to maps/city.json makes the whole file unloadable with
//
//	parse city.json: json: unknown field "_comment"
//
// which is a startup failure, not a warning. Document knobs in the README or in
// the config struct's field comments instead, and if a per-field note is truly
// needed, add a real field to CityConfig for it rather than a pseudo-key.
// (Observed for real: a "_comment" inside a new balance block broke six tests
// and the server's startup until it was removed.)
//
// Keep the strictness. A silently-zeroed `every_ticks` or `duration_ticks` is a
// far worse failure than a map that refuses to load.
func LoadCityConfig(path string) (*CityConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read city file: %w", err)
	}
	cfg, err := DecodeCityConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return cfg, nil
}

// DecodeCityConfig decodes one city document. It applies the same strictness as
// LoadCityConfig: unknown fields refuse the whole document, and every required
// knob is validated. It takes bytes so stores can decode a PostgreSQL payload
// without inventing a temporary file. The error never names a path: the caller
// knows what it read.
func DecodeCityConfig(raw []byte) (*CityConfig, error) {
	var cfg CityConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validate reports every missing or out-of-range knob in one error. It applies
// no fallbacks: the map file is the only source of truth, and a missing value
// must fail the load rather than silently run with zero. Fields whose zero is
// a meaningful choice (for example force_first_order_at_tick and
// charger_price_per_tick) document that and pass at zero.
func (cfg *CityConfig) validate() error {
	var problems []string

	positiveInt := func(v int, field string, suggested int) {
		if v <= 0 {
			problems = append(problems, fmt.Sprintf("%s must be > 0 (suggested %d)", field, suggested))
		}
	}
	nonNegativeInt := func(v int, field string, suggested int) {
		if v < 0 {
			problems = append(problems, fmt.Sprintf("%s must be >= 0 (suggested %d)", field, suggested))
		}
	}
	positiveInt64 := func(v int64, field string, suggested int64) {
		if v <= 0 {
			problems = append(problems, fmt.Sprintf("%s must be > 0 (suggested %d)", field, suggested))
		}
	}
	positiveFloat := func(v float64, field string, suggested float64) {
		if v <= 0 {
			problems = append(problems, fmt.Sprintf("%s must be > 0 (suggested %g)", field, suggested))
		}
	}
	nonNegativeFloat := func(v float64, field string, suggested float64) {
		if v < 0 {
			problems = append(problems, fmt.Sprintf("%s must be >= 0 (suggested %g)", field, suggested))
		}
	}
	fraction := func(v float64, field string, suggested float64) {
		if v <= 0 || v > 1 {
			problems = append(problems, fmt.Sprintf("%s must be in (0, 1] (suggested %g)", field, suggested))
		}
	}

	positiveInt(cfg.TickMS, "tick_ms", 600)
	nonNegativeInt(cfg.GroundExtraStepCost, "ground_extra_step_cost", 1)
	positiveInt(cfg.TicksPerMonth, "ticks_per_month", 600)
	positiveInt(cfg.MaxEvents, "max_events", 500)
	positiveFloat(cfg.MaxReputation, "max_reputation", 5)
	positiveInt(cfg.MaxChapter, "max_chapter", 5)
	positiveInt(cfg.AutosaveTicks, "autosave_ticks", 60)
	nonNegativeInt(cfg.StartMoney, "starting_money", 0)

	o := &cfg.Orders
	positiveInt(o.MaxActiveOrders, "orders.max_active_orders", 6)
	positiveFloat(o.TargetOrdersPerCustomer, "orders.target_orders_per_customer", 0.75)
	positiveInt(o.MinSpawnGapTicks, "orders.min_spawn_gap_ticks", 12)
	positiveFloat(o.DeadlineSlack, "orders.deadline_slack", 2)
	positiveFloat(o.RewardBulkBase, "orders.reward_bulk_base", 0.6)
	positiveFloat(o.RewardBulkPerBox, "orders.reward_bulk_per_box", 0.4)
	positiveInt(o.ExpireFineDivisor, "orders.expire_fine_divisor", 4)
	positiveInt64(o.MinRouteTicks, "orders.min_route_ticks", 5)
	positiveInt64(o.MinDeadlineTicks, "orders.min_deadline_ticks", 10)
	positiveInt64(o.UnreachableLegTicks, "orders.unreachable_leg_ticks", 20)
	nonNegativeInt(o.DistanceBonusPerTile, "orders.distance_bonus_per_tile", 3)
	nonNegativeFloat(o.ExpirePenaltyReputation, "orders.expire_penalty_reputation", 0.10)
	fraction(o.LatePayMultiplier, "orders.late_pay_multiplier", 0.6)
	nonNegativeInt(o.ForceFirstOrderAtTick, "orders.force_first_order_at_tick", 0)
	nonNegativeFloat(o.ReputationPerDelivery, "orders.reputation_per_delivery", 0.02)
	nonNegativeFloat(o.ReputationPerLate, "orders.reputation_per_late", 0.05)
	positiveInt64(o.RampTicks, "orders.ramp_ticks", 2400)
	fraction(o.CustomerCooldownMinScale, "orders.customer_cooldown_min_scale", 0.5)
	for i, v := range o.DeadlineSlackByChapter {
		positiveFloat(v, fmt.Sprintf("orders.deadline_slack_by_chapter[%d]", i), 2)
	}
	for i, v := range o.ChapterRewardMultiplier {
		positiveFloat(v, fmt.Sprintf("orders.chapter_reward_multiplier[%d]", i), 1)
	}
	for i, b := range o.QuantityBands {
		prefix := fmt.Sprintf("orders.quantity_bands[%d]", i)
		positiveInt(b.MinSize, prefix+".min_size", 1)
		positiveInt(b.MaxSize, prefix+".max_size", b.MinSize)
		if b.MaxSize > 0 && b.MinSize > 0 && b.MaxSize < b.MinSize {
			problems = append(problems, fmt.Sprintf("%s.max_size (%d) must be >= min_size (%d)", prefix, b.MaxSize, b.MinSize))
		}
		if b.FromProgress < 0 || b.FromProgress > 1 {
			problems = append(problems, fmt.Sprintf("%s.from_progress must be in [0, 1] (suggested 0)", prefix))
		}
		nonNegativeInt(b.EarlyWeight, prefix+".early_weight", 0)
		positiveInt(b.Weight, prefix+".weight", 30)
	}

	positiveInt(cfg.Charging.DepotRate, "charging.depot_rate", 6)
	positiveInt(cfg.Charging.ChargerRate, "charging.charger_rate", 2)
	nonNegativeInt(cfg.Charging.ChargerPricePerTick, "charging.charger_price_per_tick", 1)
	for i, ch := range cfg.Chargers {
		prefix := fmt.Sprintf("chargers[%d]", i)
		positiveInt(ch.Capacity, prefix+".capacity", 1)
		nonNegativeInt(ch.Rate, prefix+".rate", 0)
		if ch.PricePerTick != nil {
			nonNegativeInt(*ch.PricePerTick, prefix+".price_per_tick", 0)
		}
	}
	for i, d := range cfg.Depots {
		prefix := fmt.Sprintf("depots[%d]", i)
		nonNegativeInt(d.ChargeSlots, prefix+".charge_slots", 4)
	}
	for class, cc := range cfg.CarClasses {
		prefix := "car_classes." + class
		positiveInt(cc.Capacity, prefix+".capacity", 4)
		positiveInt(cc.MaxBattery, prefix+".max_battery", 24)
		positiveInt(cc.StepCost, prefix+".step_cost", 1)
		nonNegativeInt(cc.Price, prefix+".price", 0)
		nonNegativeInt(cc.UpkeepPerInterval, prefix+".upkeep_per_interval", 0)
	}

	if cfg.GridSize > 0 && len(cfg.Layout) > 0 && len(cfg.Legend) == 0 {
		problems = append(problems, "legend is empty; every layout char needs a terrain")
	}
	if cfg.Fleet == nil {
		problems = append(problems, "fleet block is required (set upkeep_interval_ticks to 0 to disable upkeep)")
	} else {
		nonNegativeInt(cfg.Fleet.UpkeepIntervalTicks, "fleet.upkeep_interval_ticks", 120)
		nonNegativeInt(cfg.Fleet.DefaultUpkeepPerInterval, "fleet.default_upkeep_per_interval", 1)
		positiveInt(cfg.Fleet.UpkeepBoxDivisor, "fleet.upkeep_box_divisor", 4)
		nonNegativeInt(cfg.Fleet.UpkeepPerBoxPerInterval, "fleet.upkeep_per_box_per_interval", 1)
		nonNegativeInt(cfg.Fleet.TowFeePerTile, "fleet.tow_fee_per_tile", 2)
		nonNegativeInt(cfg.Fleet.TowFeeMin, "fleet.tow_fee_min", 25)
	}
	if cfg.Closures == nil {
		problems = append(problems, "closures block is required (set enabled to false for a quiet map)")
	} else if cfg.Closures.Enabled {
		nonNegativeInt(cfg.Closures.FromChapter, "closures.from_chapter", 1)
		positiveInt(cfg.Closures.EveryTicks, "closures.every_ticks", 900)
		positiveInt(cfg.Closures.DurationTicks, "closures.duration_ticks", 260)
		positiveInt(cfg.Closures.MaxConcurrent, "closures.max_concurrent", 1)
		positiveInt(cfg.Closures.SegmentTiles, "closures.segment_tiles", 3)
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid city config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// validateRewardCoverage is the publish/startup half of validation: every item
// declared in Items needs a positive reward_base, and every reward_base entry
// must name a real item. Drafts skip this so an author can add an item before
// pricing it; a map that reaches NewWorld or publish must cover it because the
// engine has no fallback price.
func (cfg *CityConfig) validateRewardCoverage() error {
	var problems []string
	for id := range cfg.Items {
		if cfg.Orders.RewardBase[id] <= 0 {
			problems = append(problems, fmt.Sprintf("orders.reward_base.%s must be > 0 (suggested 40)", id))
		}
	}
	for id := range cfg.Orders.RewardBase {
		if _, ok := cfg.Items[id]; !ok {
			problems = append(problems, fmt.Sprintf("orders.reward_base.%s has no matching items entry", id))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid city rewards:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// CityFromFile loads a builtin city file, validates its required knobs, builds
// the City (including its initial fog), and runs the full startup validation.
// It returns the decoded config as well because NewWorld needs the pacing,
// fleet and growth rules that the City itself does not carry.
//
// Community payloads do not come from disk: decode them with DecodeCityConfig
// and build with CityFromConfig. Errors from this function name the file or
// city so a broken builtin fails startup with its identity attached.
func CityFromFile(path string) (*City, *CityConfig, error) {
	cfg, err := LoadCityConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.validateRewardCoverage(); err != nil {
		return nil, nil, fmt.Errorf("city %q: %w", cfg.Name, err)
	}
	city, err := CityFromConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("city %q: %w", cfg.Name, err)
	}
	return city, cfg, nil
}

// CityFromConfig builds the runtime City for a decoded config, including the
// fog-of-war districts the session starts with. It is the config-only half of
// CityFromFile, for a catalog that has already decoded a city from PostgreSQL
// or from an in-memory builtin.
func CityFromConfig(cfg *CityConfig) (*City, error) {
	city, err := NewCity(cfg)
	if err != nil {
		return nil, err
	}
	applyInitialHiding(city, cfg)
	return city, nil
}

// CityFromConfigStructural builds a City from grid and entity tables only. It
// is the authoring view of a DRAFT: a city may be structurally valid while
// City.Validate would still refuse it (a missing depot, an unserved demand),
// and its owner must be able to see and edit it. Published/startup paths keep
// using CityFromConfig's full validation.
func CityFromConfigStructural(cfg *CityConfig) (*City, error) {
	city, err := parseCity(cfg)
	if err != nil {
		return nil, err
	}
	applyInitialHiding(city, cfg)
	return city, nil
}

// ValidateCityConfigStructural is the DRAFT-save validator: it builds the grid
// and entity tables (parseCity) but deliberately skips City.Validate's content
// and reachability checks. A city that is structurally sound may still be
// unwinnable or reference an unserved item; that is exactly what a draft is
// allowed to be. Malformed JSON and unknown fields are already refused by
// DecodeCityConfig, before this runs.
func ValidateCityConfigStructural(cfg *CityConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil city config")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	_, err := parseCity(cfg)
	return err
}

// ValidateCityConfig is the full startup/publish validator: the same
// NewCity(cfg) plus initial-hiding path CityFromFile uses. A city that passes
// this can be simulated.
func ValidateCityConfig(cfg *CityConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil city config")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := cfg.validateRewardCoverage(); err != nil {
		return err
	}
	_, err := CityFromConfig(cfg)
	return err
}

// applyInitialHiding hides every tile that belongs to a hidden district.
//
// District membership is a set of inclusive rectangles in the map file, so the
// layout stays a plain grid of legend chars and districts are described
// declaratively rather than by overloading terrain characters.
func applyInitialHiding(city *City, cfg *CityConfig) {
	for _, d := range cfg.Districts {
		if !d.Hidden {
			continue
		}
		for _, r := range d.Rects {
			city.HideRect(r[0], r[1], r[2], r[3])
		}
	}
}

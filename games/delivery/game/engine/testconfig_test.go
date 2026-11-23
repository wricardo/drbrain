package engine

// completeConfig fills every required knob with its suggested value so a test
// that is not about config validation can state only the fields it exercises.
// Fields whose zero is a meaningful choice (distance bonus, reputation gains,
// force_first_order_at_tick, charge slots) are left untouched.
//
// Tests for validate itself must call validate directly instead of going
// through this helper.
func completeConfig(cfg *CityConfig) *CityConfig {
	if cfg.TickMS == 0 {
		cfg.TickMS = 300
	}
	if cfg.GroundExtraStepCost == 0 {
		cfg.GroundExtraStepCost = 1
	}
	if cfg.TicksPerMonth == 0 {
		cfg.TicksPerMonth = 600
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 500
	}
	if cfg.MaxReputation == 0 {
		cfg.MaxReputation = 5
	}
	if cfg.MaxChapter == 0 {
		cfg.MaxChapter = 5
	}
	if cfg.AutosaveTicks == 0 {
		cfg.AutosaveTicks = 60
	}
	if cfg.Charging.DepotRate == 0 {
		cfg.Charging.DepotRate = 6
	}
	if cfg.Charging.ChargerRate == 0 {
		cfg.Charging.ChargerRate = 2
	}
	if cfg.Fleet == nil {
		cfg.Fleet = &FleetConfig{
			UpkeepIntervalTicks:      0, // keep the pre-upkeep test economy by default
			UpkeepBoxDivisor:         4,
			UpkeepPerBoxPerInterval:  1,
			DefaultUpkeepPerInterval: 1,
		}
	}
	if cfg.Closures == nil {
		cfg.Closures = &ClosureConfig{}
	}

	o := &cfg.Orders
	if o.MaxActiveOrders == 0 {
		o.MaxActiveOrders = 6
	}
	if o.TargetOrdersPerCustomer == 0 {
		o.TargetOrdersPerCustomer = 0.75
	}
	if o.MinSpawnGapTicks == 0 {
		o.MinSpawnGapTicks = 12
	}
	if o.DeadlineSlack == 0 {
		o.DeadlineSlack = 2
	}
	if o.RewardBulkBase == 0 {
		o.RewardBulkBase = 0.6
	}
	if o.RewardBulkPerBox == 0 {
		o.RewardBulkPerBox = 0.4
	}
	if o.ExpireFineDivisor == 0 {
		o.ExpireFineDivisor = 4
	}
	if o.MinRouteTicks == 0 {
		o.MinRouteTicks = 5
	}
	if o.MinDeadlineTicks == 0 {
		o.MinDeadlineTicks = 10
	}
	if o.UnreachableLegTicks == 0 {
		o.UnreachableLegTicks = 20
	}
	if o.LatePayMultiplier == 0 {
		o.LatePayMultiplier = 0.6
	}
	if o.RampTicks == 0 {
		o.RampTicks = 2400
	}
	if o.CustomerCooldownMinScale == 0 {
		o.CustomerCooldownMinScale = 0.5
	}
	if o.RewardBase == nil {
		o.RewardBase = map[string]int{}
	}
	for id := range cfg.Items {
		if o.RewardBase[id] == 0 {
			o.RewardBase[id] = 40
		}
	}

	for i := range cfg.Chargers {
		if cfg.Chargers[i].Capacity == 0 {
			cfg.Chargers[i].Capacity = 1
		}
	}
	for class, cc := range cfg.CarClasses {
		if cc.Capacity == 0 {
			cc.Capacity = 4
		}
		if cc.MaxBattery == 0 {
			cc.MaxBattery = 24
		}
		if cc.StepCost == 0 {
			cc.StepCost = 1
		}
		cfg.CarClasses[class] = cc
	}
	return cfg
}

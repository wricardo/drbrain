package engine

import "testing"

// TestCustomerCooldownShrinksWithProgress pins the tick-percentage customer
// schedule: the same site waits less at 100% ramp than at 0%, independent of
// chapter.
func TestCustomerCooldownShrinksWithProgress(t *testing.T) {
	w := orderWorld(t)
	w.ordersCfg.RampTicks = 100
	w.ordersCfg.CustomerCooldownMinScale = 0.5
	cu := w.City.Customers[0]

	w.Tick = 0
	w.advanceCooldown(cu, "pizza")
	slow := w.spawn.NextEligibleTick[cu.ID] - w.Tick

	w.Tick = 100
	w.advanceCooldown(cu, "pizza")
	fast := w.spawn.NextEligibleTick[cu.ID] - w.Tick

	if slow != 20 {
		t.Fatalf("cooldown at 0%% ramp = %d, want 20", slow)
	}
	if fast != 10 {
		t.Fatalf("cooldown at 100%% ramp = %d, want 10", fast)
	}
	if got := w.Progress(); got != 1 {
		t.Fatalf("progress at ramp end = %v, want 1", got)
	}
}

// TestTargetActiveGrowsWithTicksWithoutCap pins the unbounded term: with no
// MaxActiveOrders ceiling the governor keeps raising the queue target as the
// clock advances, long after the population and ramp have settled.
func TestTargetActiveGrowsWithTicksWithoutCap(t *testing.T) {
	w := orderWorld(t)
	w.ordersCfg.MaxActiveOrders = 0
	w.ordersCfg.TargetGrowthPer1000Ticks = 0.5

	for _, c := range []struct {
		tick int64
		want int
	}{
		{0, 1},       // one customer at ratio 1.0
		{2000, 2},    // +1 from the time term
		{10000, 6},   // +5
		{100000, 51}, // +50, no ceiling
	} {
		w.Tick = c.tick
		if got := w.targetActive(); got != c.want {
			t.Fatalf("tick %d: target = %d, want %d", c.tick, got, c.want)
		}
	}
}

// TestPositiveMaxActiveOrdersStillCapsTarget keeps the opt-in ceiling honest:
// a map that sets a positive cap keeps it even against the time term.
func TestPositiveMaxActiveOrdersStillCapsTarget(t *testing.T) {
	w := orderWorld(t)
	w.ordersCfg.MaxActiveOrders = 2
	w.ordersCfg.TargetGrowthPer1000Ticks = 0.5
	w.Tick = 100000
	if got := w.targetActive(); got != 2 {
		t.Fatalf("target = %d, want the explicit cap 2", got)
	}
}

// TestCooldownDecayReachesOneTickFloor pins the arrival-rate half of the
// endless curve: cooldowns keep shrinking after the ramp, but never below one
// tick.
func TestCooldownDecayReachesOneTickFloor(t *testing.T) {
	w := orderWorld(t)
	w.ordersCfg.RampTicks = 1
	w.ordersCfg.CustomerCooldownMinScale = 1 // isolate the decay from the ramp
	w.ordersCfg.CooldownDecayPer1000Ticks = 1
	cu := w.City.Customers[0]

	for _, c := range []struct {
		tick int64
		want int64
	}{
		{0, 20},
		{5000, 15},
		{100000, 1}, // clamped at the floor
	} {
		w.Tick = c.tick
		w.advanceCooldown(cu, "pizza")
		if got := w.spawn.NextEligibleTick[cu.ID] - w.Tick; got != c.want {
			t.Fatalf("tick %d: cooldown = %d, want %d", c.tick, got, c.want)
		}
	}
}

// TestShippedCityDemandKeepsGrowing is the end-to-end guard for the shipped
// configuration: no cap, a positive time term, and a cooldown that keeps
// shrinking, so target depth rises at every later tick.
func TestShippedCityDemandKeepsGrowing(t *testing.T) {
	city, cfg := shippedCity(t)
	if cfg.Orders.MaxActiveOrders != 0 {
		t.Fatalf("shipped max_active_orders = %d, want 0 (no ceiling)", cfg.Orders.MaxActiveOrders)
	}
	if cfg.Orders.TargetGrowthPer1000Ticks <= 0 || cfg.Orders.CooldownDecayPer1000Ticks <= 0 {
		t.Fatalf("shipped demand growth knobs = %g/%g, want both positive",
			cfg.Orders.TargetGrowthPer1000Ticks, cfg.Orders.CooldownDecayPer1000Ticks)
	}

	w, err := NewWorldSeeded(city, cfg, 0x5eed5eed)
	if err != nil {
		t.Fatal(err)
	}
	prev := w.TargetActive()
	for _, tick := range []int64{3000, 6000, 12000, 60000} {
		w.Tick = tick
		w.applyGrowth()
		got := w.TargetActive()
		if got <= prev {
			t.Fatalf("tick %d: target = %d, want more than %d", tick, got, prev)
		}
		prev = got
	}

	cu := w.City.Customers[0]
	w.Tick = 0
	w.advanceCooldown(cu, "pizza")
	early := w.spawn.NextEligibleTick[cu.ID]
	w.Tick = 20000
	w.advanceCooldown(cu, "pizza")
	late := w.spawn.NextEligibleTick[cu.ID] - w.Tick
	if early <= late {
		t.Fatalf("cooldown at tick 0 = %d, at 20000 = %d; want it to shrink", early, late)
	}
}

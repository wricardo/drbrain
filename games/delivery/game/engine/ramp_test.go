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

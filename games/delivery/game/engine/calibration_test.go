package engine

import "testing"

// TestShippedOrderLadderRampsDemand pins the intended progress curve on the
// real map: scooter-sized work at the start, van-sized work once the ramp opens
// enough of the city, and truck-sized work only late. The truck itself is
// money-only from the start; this test covers the demand ladder, not vehicle
// purchase permission. Demand is allowed to exceed the current fleet; that is
// the pressure to upgrade rather than a spawner bug.
func TestShippedOrderLadderRampsDemand(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	cu := w.City.Customers[0]
	ramp := w.ordersCfg.RampTicks
	if ramp <= 0 {
		t.Fatal("ramp ticks not configured")
	}

	maxAt := func(progress float64) int {
		w.Tick = int64(progress * float64(ramp))
		biggest := 0
		for seed := uint64(0); seed < 4000; seed++ {
			w.Seed = seed
			if q := w.rollQuantity(cu); q > biggest {
				biggest = q
			}
		}
		return biggest
	}

	if got := maxAt(0); got > 3 {
		t.Fatalf("ramp 0 saw %d boxes; scooter work must stay at 1-3", got)
	}
	if got := maxAt(0.175); got < 5 || got > 10 {
		t.Fatalf("ramp 17.5%% largest = %d boxes, want van work 5-10", got)
	}
	if got := maxAt(0.30); got < 13 || got > 24 {
		t.Fatalf("ramp 30%% largest = %d boxes, want large-van work 13-24", got)
	}
	if got := maxAt(0.69); got > 24 {
		t.Fatalf("ramp 69%% saw %d boxes before the late band", got)
	}
	if got := maxAt(0.70); got < 30 {
		t.Fatalf("ramp 70%% largest = %d boxes, want truck work 30-40", got)
	}

	// Demand itself must follow the scheduled population, not the fleet. The
	// downtown starts with two sites; the early growth stages add three more,
	// the harbour adds two, and the hills add two.
	w2, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := w2.TargetActive(); got != 1 {
		t.Fatalf("initial target = %d, want 1 from two downtown customers at 0.6", got)
	}
	wantCustomers := []struct {
		tick int64
		next int
	}{
		{180, 3},   // Corner Cafe
		{420, 5},   // harbour reveal: Dockside + Lighthouse
		{720, 6},   // Market Flats in the harbour
		{1200, 9},  // hills reveal + Office Block
		{1680, 10}, // Ridge House
		{2400, 11}, // Night Flat
	}
	for _, step := range wantCustomers {
		w2.Tick = step.tick
		w2.applyGrowth()
		visible := 0
		for _, cu := range w2.City.Customers {
			if !w2.City.IsHidden(cu.Pos) {
				visible++
			}
		}
		if visible != step.next {
			t.Fatalf("visible customers after tick %d = %d, want %d", step.tick, visible, step.next)
		}
	}
	if got := w2.TargetActive(); got != 7 {
		t.Fatalf("late-game target = %d, want 7 from eleven customers at 0.6", got)
	}
}

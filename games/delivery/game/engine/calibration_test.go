package engine

import (
	"path/filepath"
	"testing"
)

// TestShippedOrderLadderKeepsVehiclesRelevant pins the quantity contract
// shared by the shipped maps:
//
//   - bands with an EarlyWeight roll from tick 0, so scooter- and van-sized
//     work exists at every stage instead of the small bands switching off once
//     the ramp finishes;
//   - the truck band (30-40) has no early floor: it is deliberately absent
//     until its gate, because a truck cannot be afforded at tick 0 and every
//     early truck order would expire;
//   - the opening is still dominated by scooter work;
//   - a band's gate still matters: its share jumps when FromProgress is
//     reached.
//
// Demand is allowed to exceed the current fleet; that is the pressure to
// upgrade rather than a spawner bug.
func TestShippedOrderLadderKeepsVehiclesRelevant(t *testing.T) {
	const rolls = 4000
	for _, name := range []string{"city.json", "saopaulo.json"} {
		t.Run(name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "..", "maps", name))
			if err != nil {
				t.Fatal(err)
			}
			city, cfg, err := CityFromFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			w, err := NewWorldSeeded(city, cfg, 1)
			if err != nil {
				t.Fatal(err)
			}
			bands := cfg.Orders.QuantityBands
			if len(bands) < 2 {
				t.Fatalf("%s has %d quantity bands; expected the full ladder", name, len(bands))
			}
			if cfg.Orders.RampTicks <= 0 {
				t.Fatal("ramp ticks not configured")
			}

			countsAt := func(progress float64) []int {
				w.Tick = int64(progress * float64(cfg.Orders.RampTicks))
				counts := make([]int, len(bands))
				for seed := uint64(0); seed < rolls; seed++ {
					w.Seed = seed
					q := w.rollQuantity(w.City.Customers[0])
					idx := -1
					for i, b := range bands {
						if q >= b.MinSize && q <= b.MaxSize {
							idx = i
							break
						}
					}
					if idx < 0 {
						t.Fatalf("%s: rolled %d boxes at progress %.2f, outside every band", name, q, progress)
					}
					counts[idx]++
				}
				return counts
			}

			// Every eligible band must roll, and a deliberately gated band (no
			// EarlyWeight) must stay out until its gate. That is the difference
			// between "all ranges everywhere" and "all ranges that the fleet
			// can already serve".
			for _, progress := range []float64{0, 0.5, 1} {
				counts := countsAt(progress)
				for i, b := range bands {
					eligible := progress >= b.FromProgress || b.EarlyWeight > 0
					if eligible && counts[i] == 0 {
						t.Fatalf("%s: band %d-%d never rolled at progress %.2f; eligible bands must offer work",
							name, b.MinSize, b.MaxSize, progress)
					}
					if !eligible && counts[i] != 0 {
						t.Fatalf("%s: band %d-%d rolled %d times at progress %.2f before its %.2f gate",
							name, b.MinSize, b.MaxSize, counts[i], progress, b.FromProgress)
					}
				}
				// The scooter-sized band must stay a real share of the work,
				// otherwise late-game play would have no use for a scooter.
				if counts[0]*100 < 25*rolls {
					t.Fatalf("%s: scooter band is %.1f%% of rolls at progress %.2f; want at least 25%%",
						name, 100*float64(counts[0])/float64(rolls), progress)
				}
			}

			// The opening is still mostly scooter work: early weights are a
			// floor, not an equal share, so a new player is not flooded with
			// vans they cannot buy yet.
			if opening := countsAt(0)[0]; opening*100 < 85*rolls {
				t.Fatalf("%s: scooter band is %.1f%% of opening rolls; want at least 85%%",
					name, 100*float64(opening)/float64(rolls))
			}

			// Reaching FromProgress must escalate the band. A band with an
			// early floor grows sharply; a hard-gated band appears at all.
			for i, b := range bands {
				if b.FromProgress <= 0 || b.FromProgress >= 1 {
					continue
				}
				before := countsAt(b.FromProgress - 0.01)[i]
				after := countsAt(b.FromProgress)[i]
				if after <= before {
					t.Fatalf("%s: band %d-%d share did not escalate at progress %.2f: %d -> %d rolls",
						name, b.MinSize, b.MaxSize, b.FromProgress, before, after)
				}
				if b.EarlyWeight > 0 && after < before*5 {
					t.Fatalf("%s: band %d-%d share grew too little at progress %.2f: %d -> %d rolls",
						name, b.MinSize, b.MaxSize, b.FromProgress, before, after)
				}
			}
		})
	}
}

// TestShippedCityDemandFollowsPopulation guards queue depth on the shipped map:
// it follows the scheduled city population, not the player's fleet. The
// unbounded time term is switched off here so this test measures the
// population anchor alone; TestShippedCityDemandKeepsGrowing covers the time
// term.
func TestShippedCityDemandFollowsPopulation(t *testing.T) {
	city, cfg := shippedCity(t)
	cfg.Orders.TargetGrowthPer1000Ticks = 0
	w, err := NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The downtown starts with two sites; the early growth stages add three
	// more, the harbour adds two, and the hills add two.
	if got := w.TargetActive(); got != 1 {
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
		w.Tick = step.tick
		w.applyGrowth()
		visible := 0
		for _, cu := range w.City.Customers {
			if !w.City.IsHidden(cu.Pos) {
				visible++
			}
		}
		if visible != step.next {
			t.Fatalf("visible customers after tick %d = %d, want %d", step.tick, visible, step.next)
		}
	}
	if got := w.TargetActive(); got != 7 {
		t.Fatalf("late-game target = %d, want 7 from eleven customers at 0.6", got)
	}
}

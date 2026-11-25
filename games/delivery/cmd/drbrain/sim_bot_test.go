package main

import (
	"fmt"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// TestBotKeepsFleetMoving is the floor under the pacing table: the scripted bot
// must not deadlock or starve on the shipped map. Seed 1 used to lock a scooter
// and a van head-on in a one-lane street for 2000+ ticks (45 delivered, 184
// expired); every seed here delivers 280 to 330 and expires under 40 today, so
// the floors below leave wide margin yet fail on any such collapse.
func TestBotKeepsFleetMoving(t *testing.T) {
	city := cliCity(t)
	for _, seed := range []uint64{1, 2, 0x5eed5eed} {
		t.Run(fmt.Sprintf("seed_%#x", seed), func(t *testing.T) {
			c, cfg, err := engine.CityFromFile(city)
			if err != nil {
				t.Fatal(err)
			}
			w, err := engine.NewWorldSeeded(c, cfg, seed)
			if err != nil {
				t.Fatal(err)
			}
			for range 3000 {
				botStep(w)
				w.Advance()
			}
			l := w.Ledger
			if l.Deliveries < 200 {
				t.Errorf("delivered %d, want >= 200 (expired %d, cars %d)", l.Deliveries, l.ExpiredOrders, len(w.Cars))
			}
			if l.ExpiredOrders > 80 {
				t.Errorf("expired %d, want <= 80 (delivered %d)", l.ExpiredOrders, l.Deliveries)
			}
		})
	}
}

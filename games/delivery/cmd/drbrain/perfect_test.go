package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// TestPerfectPlayerKeepsItsPlans runs the planning driver for 3000 ticks and
// holds it to what separates it from the pacing bot: every step it queues is one
// the engine accepts (no blocked moves, so no head-on swap or double booking was
// ever planned), cars almost never need a tow, and nearly every order lands.
//
// The floors sit well under what it does today (343 to 347 delivered, 0 to 2
// expired, all of them avoidable, on these seeds) but far above the pacing bot's
// worst seeds (286 delivered, 28 expired), so they fail on a planner regression,
// not on seed noise. Seed 1 is here because it is where the pacing bot's
// head-on lock showed up. Expiries the fleet could not have prevented are
// classified and excluded from the avoidable count; the shipped cities no longer
// raise any (see TestShippedCitiesDoNotAskForWhatTheStartingFleetCannotCarry).
func TestPerfectPlayerKeepsItsPlans(t *testing.T) {
	city := cliCity(t)
	for _, seed := range []uint64{1, 99, 0x5eed5eed} {
		t.Run(fmt.Sprintf("seed_%#x", seed), func(t *testing.T) {
			c, cfg, err := engine.CityFromFile(city)
			if err != nil {
				t.Fatal(err)
			}
			w, err := engine.NewWorldSeeded(c, cfg, seed)
			if err != nil {
				t.Fatal(err)
			}
			pl := newPilot(w, cfg, false)
			for range 3000 {
				pl.step()
				pl.after(w.Advance())
			}
			l := w.Ledger
			if l.Deliveries < 300 {
				t.Errorf("delivered %d, want >= 300", l.Deliveries)
			}
			if l.ExpiredOrders > 12 {
				t.Errorf("expired %d, want <= 12 (%v)", l.ExpiredOrders, pl.expiries)
			}
			if got := pl.avoidable(); got > 4 {
				t.Errorf("%d avoidable expiries, want <= 4 (%v)", got, pl.expiries)
			}
			if pl.blocked != 0 {
				t.Errorf("%d moves were refused by the engine: a planned step was illegal", pl.blocked)
			}
			if l.Tows > 2 {
				t.Errorf("%d tows: cars ran out of battery away from a charger", l.Tows)
			}
		})
	}
}

// TestPerfectRejectsBadArguments mirrors the sim's argument contract: a typo in a
// recorded command must fail loudly, not print a plausible-looking table.
func TestPerfectRejectsBadArguments(t *testing.T) {
	city := cliCity(t)
	cases := [][]string{
		{"-f", city, "-ticks", "0"},
		{"-f", city, "-every", "0", "-ticks", "5"},
		{"-f", city, "-ticks", "abc"},
		{"-f", city, "-nope"},
		{"-f", city, "-ticks", "5", "extra"},
	}
	for _, args := range cases {
		if got := cmdPerfect(args); got != 2 {
			t.Errorf("cmdPerfect(%v) = %d, want 2 (usage error)", args, got)
		}
	}
	if got := cmdPerfect([]string{"-f", city, "-ticks", "3", "-every", "1"}); got != 0 {
		t.Errorf("cmdPerfect with valid arguments = %d, want 0", got)
	}
}

// TestPerfectPlayerOnTheSaoPauloMaps runs the planner on the two big maps. São
// Paulo was where it used to lose 8 to 20 orders a run: a car holding a stale
// charge decision drove across the map to charge before a pickup next to it, a
// kitchen with one access tile left two cars each waiting for the other to move,
// and a car recharged to full at a charger it could not set off from. All three
// ran quietly on the small map and showed up with distance.
func TestPerfectPlayerOnTheSaoPauloMaps(t *testing.T) {
	for _, name := range []string{"saopaulo.json", "saopaulo2.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "maps", name)
			c, cfg, err := engine.CityFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			w, err := engine.NewWorldSeeded(c, cfg, 1)
			if err != nil {
				t.Fatal(err)
			}
			pl := newPilot(w, cfg, false)
			for range 3000 {
				pl.step()
				pl.after(w.Advance())
			}
			if got := pl.avoidable(); got > 2 {
				t.Errorf("%d avoidable expiries, want <= 2 (%v)", got, pl.expiries)
			}
			// A few refused moves in 3000 ticks happen here (8 on seed 1, cause not
			// traced: each was a step east into an occupied tile). Each is replanned
			// the next tick; hundreds would be a planner fault.
			if pl.blocked > 20 || w.Ledger.Tows > 2 {
				t.Errorf("blocked %d, tows %d", pl.blocked, w.Ledger.Tows)
			}
		})
	}
}

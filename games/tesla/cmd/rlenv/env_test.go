package main

import (
	"math"
	"testing"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
)

func testMap(battery int) *engine.GameConfig {
	// 7x7 open room, home at (1,1), one park at (5,5): every cell is 6 moves
	// (or fewer) from home, so a short walk can drop the battery below that.
	return &engine.GameConfig{
		Name: "test-room", GridSize: 7, MaxBattery: battery, StartingBattery: battery,
		Legend: legend,
		Layout: []string{
			"BBBBBBB",
			"BHRRRRB",
			"BRRRRRB",
			"BRRRRRB",
			"BRRRRRB",
			"BRRRRPB",
			"BBBBBBB",
		},
	}
}

func newTestEnv(t *testing.T, battery int, safety float64) *env {
	t.Helper()
	e := newEnv(defaultRewards)
	e.safety = safety
	if err := e.load(testMap(battery)); err != nil {
		t.Fatalf("load: %v", err)
	}
	return e
}

func TestPotentialIsZeroWhileAChargerIsReachable(t *testing.T) {
	e := newTestEnv(t, 10, 0)
	if got := e.potential(); got != 0 {
		t.Fatalf("potential at full battery = %v, want 0", got)
	}
}

func TestPotentialGoesNegativeWhenTheBatteryCannotReachACharger(t *testing.T) {
	e := newTestEnv(t, 3, 0) // start ON home: still safe
	if got := e.potential(); got != 0 {
		t.Fatalf("potential on home = %v, want 0", got)
	}
	// walk four cells away; the nearest charger is now ~4 moves away but only
	// 2 battery remain, so the shortfall is about 2.
	for _, a := range []int{1, 1, 3, 3} { // down, down, right, right
		e.step(a)
	}
	if got := e.potential(); got >= 0 {
		t.Fatalf("potential after walking away on a low battery = %v, want negative", got)
	}
}

func TestSafetyShapingPaysForBackingOutOfDanger(t *testing.T) {
	// Same walk with and without shaping: the shaped run must score lower once
	// the car is in deficit, and the difference must be the shaping term only.
	plain := newTestEnv(t, 3, 0)
	shaped := newTestEnv(t, 3, 0.05)
	walk := []int{1, 1, 1, 3} // down, down, down, right: away from home, no parks
	var plainSum, shapedSum float64
	for _, a := range walk {
		plainSum += plain.step(a).Reward
		shapedSum += shaped.step(a).Reward
	}
	if shapedSum >= plainSum {
		t.Fatalf("shaped total %.3f should be below unshaped %.3f once the battery is short", shapedSum, plainSum)
	}
	if diff := plainSum - shapedSum; diff <= 0 || diff > 1 {
		t.Fatalf("shaping changed the return by %.3f, expected a small penalty in (0,1]", diff)
	}
	if math.Abs(shaped.prevPhi) == 0 {
		t.Log("still safe after the walk; shaping had nothing to say")
	}
}

func TestFogHidesUnseenCellsAndAllowsExploringThem(t *testing.T) {
	e := newTestEnv(t, 10, 0)
	e.fog = 2
	if err := e.load(testMap(10)); err != nil {
		t.Fatalf("load: %v", err)
	}
	buf := make([]byte, NumPlanes*GridPad*GridPad)
	plane := func(p, x, y int) byte { return buf[p*GridPad*GridPad+y*GridPad+x] }
	e.observe(buf)

	// the park at (5,5) is four cells from home (1,1): outside a radius-2 view
	if plane(planeUnknown, 5, 5) != 1 {
		t.Fatalf("park cell should be unknown at reset with fog 2")
	}
	if plane(planePark, 5, 5) != 0 {
		t.Fatalf("unknown park must not leak its type")
	}
	if plane(planeUnknown, 1, 1) != 0 {
		t.Fatalf("the cell the car is on must be known")
	}

	// unexplored cells are offered as legal moves: exploring them is the risk
	m, any := e.mask()
	if !any {
		t.Fatalf("no legal moves at reset")
	}
	if !m[0] && !m[1] && !m[2] && !m[3] {
		t.Fatalf("mask rejected every direction")
	}

	// driving reveals the cells around the new position
	for range 4 { // four steps right along row 1
		e.step(3)
	}
	e.observe(buf)
	if e.sees(5, 1) != true {
		t.Fatalf("driven-over cells must become known")
	}
	if plane(planeUnknown, 5, 5) != 1 {
		t.Fatalf("the park four rows away must still be unknown after a 4-cell drive")
	}
}

func TestFogOffMeansEverythingIsVisible(t *testing.T) {
	e := newTestEnv(t, 10, 0)
	buf := make([]byte, NumPlanes*GridPad*GridPad)
	e.observe(buf)
	for i := range GridPad * GridPad {
		if buf[planeUnknown*GridPad*GridPad+i] != 0 {
			t.Fatalf("no cell may be unknown when fog is off")
		}
	}
}

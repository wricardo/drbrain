package engine

import (
	"strings"
	"testing"
)

// TestGrowthReportsAppliedStages: the timeline marks exactly the stages at the
// head of the schedule that have run, in order.
func TestGrowthReportsAppliedStages(t *testing.T) {
	w := tinyWorld(t)
	w.growth = []GrowthConfig{
		{ID: "g1", Tick: 10, Title: "One", Description: "first"},
		{ID: "g2", Tick: 20, Title: "Two", Description: "second"},
	}
	w.growthApplied = 1

	got := w.Growth()
	if len(got) != 2 {
		t.Fatalf("stages = %d, want 2", len(got))
	}
	if got[0].ID != "g1" || got[0].Tick != 10 || got[0].Title != "One" || got[0].Description != "first" {
		t.Errorf("status = %+v, want the authored fields", got[0])
	}
	if !got[0].Done {
		t.Error("the applied stage must be marked done")
	}
	if got[1].Done {
		t.Error("the pending stage must not be marked done")
	}
}

// TestApplyGrowthSkipsFailedStageAtomically: a stage with a bad effect is
// skipped as a whole with an event, and the rest of the schedule still applies.
func TestApplyGrowthSkipsFailedStageAtomically(t *testing.T) {
	w := tinyWorld(t)
	w.Tick = 5
	w.growth = []GrowthConfig{
		{
			ID: "bad", Tick: 0, Title: "Bad",
			Effects: []GrowthEffect{
				{Type: EffectPaveRoad, Tiles: []TileWrite{{X: 0, Y: 0, Terrain: "road"}}},
				{Type: "mystery"},
			},
		},
		{ID: "grant", Tick: 0, Title: "Grant", Effects: []GrowthEffect{{Type: EffectGrantCarClass, Class: "van"}}},
	}
	w.applyGrowth()

	if got, _ := w.City.At(Position{0, 0}); got != WaterTile {
		t.Errorf("the failed stage half-applied: (0,0) = %q, want %q", got, WaterTile)
	}
	if w.growthApplied != 2 {
		t.Fatalf("applied stages = %d, want 2 (a failed stage still advances)", w.growthApplied)
	}
	if err := w.BuyCarClass("van"); err != nil {
		t.Errorf("the stage after the failure did not apply: %v", err)
	}
	if len(w.Events) < 2 || !strings.Contains(w.Events[0].Message, "could not be applied") {
		t.Fatalf("events = %+v, want a failure event first", w.Events)
	}
}

// TestApplyGrowthSkipsStageThatWouldStrand: an effect that lands but disconnects
// a kitchen is rejected without touching the live city.
func TestApplyGrowthSkipsStageThatWouldStrand(t *testing.T) {
	city, cfg := shippedCity(t)
	w, err := NewWorldSeeded(city, cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.City.Kitchens) == 0 {
		t.Fatal("shipped city has no kitchens")
	}
	kitchen := w.City.Kitchens[0].Pos
	var walls []TileWrite
	for _, d := range AllDirections {
		if n, ok := kitchen.Neighbour(d); ok {
			walls = append(walls, TileWrite{X: n.X, Y: n.Y, Terrain: "water"})
		}
	}
	first := Position{walls[0].X, walls[0].Y}
	before, _ := w.City.At(first)

	w.growth = []GrowthConfig{{
		ID: "wall", Tick: 0, Title: "Wall",
		Effects: []GrowthEffect{{Type: EffectPaveRoad, Tiles: walls}},
	}}
	w.growthApplied = 0
	w.Tick = 0
	w.applyGrowth()

	if w.growthApplied != 1 {
		t.Fatalf("applied stages = %d, want 1", w.growthApplied)
	}
	if after, _ := w.City.At(first); after != before {
		t.Errorf("a stranding stage changed the city: %q -> %q", before, after)
	}
	if len(w.Events) == 0 || !strings.Contains(w.Events[len(w.Events)-1].Message, "would strand") {
		t.Fatalf("events = %+v, want a would-strand event", w.Events)
	}
}

// TestValidateGrowthScheduleRejectsBadSchedules pins the authoring checks that
// must fail startup before a session runs a schedule it cannot finish.
func TestValidateGrowthScheduleRejectsBadSchedules(t *testing.T) {
	cases := []struct {
		name   string
		growth []GrowthConfig
		want   string
	}{
		{"missing id", []GrowthConfig{{Tick: 0}}, "has no id"},
		{"duplicate id", []GrowthConfig{{ID: "g", Tick: 0}, {ID: "g", Tick: 1}}, "duplicate growth stage id"},
		{"descending tick", []GrowthConfig{{ID: "a", Tick: 5}, {ID: "b", Tick: 4}}, "before the previous stage"},
		{"unknown effect", []GrowthConfig{{ID: "a", Effects: []GrowthEffect{{Type: "mystery"}}}}, "growth stage"},
	}
	for _, tc := range cases {
		w := tinyWorld(t)
		w.growth = tc.growth
		err := w.validateGrowthSchedule()
		if err == nil {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestDistrictHiddenAndLookupMisses covers the fog predicate and the id lookups
// used by deadline refreshes, including the not-found answers.
func TestDistrictHiddenAndLookupMisses(t *testing.T) {
	city, cfg := shippedCity(t)
	w := &World{City: city}

	harbour, ok := cfg.Districts["harbour"]
	if !ok || len(harbour.Rects) == 0 {
		t.Fatal("shipped city has no harbour district")
	}
	if !w.districtHidden(city.Districts["harbour"]) {
		t.Error("the authored-hidden harbour district must report hidden")
	}
	r := harbour.Rects[0]
	city.RevealRect(r[0], r[1], r[2], r[3])
	if w.districtHidden(city.Districts["harbour"]) {
		t.Error("a revealed district must not report hidden")
	}
	// Rectangles entirely off-grid cover no tiles, so they stay "hidden".
	offGrid := District{Rects: [][4]int{{-5, -5, -1, -1}}}
	if !w.districtHidden(offGrid) {
		t.Error("an off-grid rect must not count as revealed")
	}

	if len(city.Kitchens) > 0 {
		if _, ok := w.kitchenByID(city.Kitchens[0].ID); !ok {
			t.Error("an existing kitchen id must resolve")
		}
	}
	if _, ok := w.kitchenByID("nope"); ok {
		t.Error("an unknown kitchen id must not resolve")
	}
	if len(city.Customers) > 0 {
		if _, ok := w.customerByID(city.Customers[0].ID); !ok {
			t.Error("an existing customer id must resolve")
		}
	}
	if _, ok := w.customerByID("nope"); ok {
		t.Error("an unknown customer id must not resolve")
	}
}

// TestCarClassCatalogOrdersAndSeparatesFlags: the catalogue is cheapest first
// with a stable name tie-break, and "unlocked" must be independent of
// "affordable".
func TestCarClassCatalogOrdersAndSeparatesFlags(t *testing.T) {
	w := &World{
		carClasses: map[string]CarClassConfig{
			"van":   {Price: 100, Capacity: 10, MaxBattery: 20, StepCost: 1},
			"bike":  {Price: 100, Capacity: 2, MaxBattery: 8, StepCost: 1},
			"truck": {Price: 3000, Capacity: 40, MaxBattery: 48, StepCost: 1, RequiresGrant: true},
		},
		buyableClasses: map[string]bool{},
		Ledger:         &Ledger{Money: 150},
	}

	got := w.CarClassCatalog()
	if len(got) != 3 {
		t.Fatalf("catalogue = %+v, want 3 entries", got)
	}
	if got[0].Class != "bike" || got[1].Class != "van" || got[2].Class != "truck" {
		t.Fatalf("order = %q, %q, %q; want bike, van, truck", got[0].Class, got[1].Class, got[2].Class)
	}
	if !got[0].Unlocked || !got[0].Affordable {
		t.Errorf("bike = %+v, want unlocked and affordable", got[0])
	}
	if got[2].Unlocked {
		t.Error("a gated class without a grant must report locked")
	}
	if got[2].Affordable {
		t.Error("a class above the balance must report unaffordable")
	}

	if err := w.BuyCarClass("nope"); err == nil {
		t.Error("an unknown class must not be buyable")
	}
	if err := w.BuyCarClass("truck"); err == nil {
		t.Error("a gated class must stay locked until a grant lands")
	}
	w.buyableClasses["truck"] = true
	if err := w.BuyCarClass("truck"); err != nil {
		t.Errorf("a granted class must be buyable: %v", err)
	}
}

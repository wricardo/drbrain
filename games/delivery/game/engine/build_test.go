package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestApplyEffectRejectsMalformedEffects: every authoring error must fail the
// stage with a message that names what is wrong, because a stage is applied
// atomically and a silent skip would hide a broken map file.
func TestApplyEffectRejectsMalformedEffects(t *testing.T) {
	raw := func(v any) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	cases := []struct {
		name   string
		effect GrowthEffect
		want   string
	}{
		{"no tiles", GrowthEffect{Type: EffectPaveRoad}, "no tiles to write"},
		{"unknown terrain", GrowthEffect{Type: EffectBuildBridge, Tiles: []TileWrite{{X: 2, Y: 2, Terrain: "lava"}}}, "unknown terrain"},
		{"off grid", GrowthEffect{Type: EffectPaveRoad, Tiles: []TileWrite{{X: 99, Y: 2, Terrain: "road"}}}, "off the 7x7 grid"},
		{"unknown district", GrowthEffect{Type: EffectRevealDistrict, District: "harbour"}, "no district named"},
		{"bad kitchen", GrowthEffect{Type: EffectAddKitchen, Entities: []json.RawMessage{json.RawMessage("{")}}, "bad kitchen"},
		{"bad customer", GrowthEffect{Type: EffectAddCustomer, Entities: []json.RawMessage{json.RawMessage("{")}}, "bad customer"},
		{"bad charger", GrowthEffect{Type: EffectAddCharger, Entities: []json.RawMessage{json.RawMessage("{")}}, "bad charger"},
		{"charger without capacity", GrowthEffect{Type: EffectAddCharger, Entities: []json.RawMessage{raw(ChargerConfig{ID: "ch1", Pos: Position{3, 3}})}}, "must be positive"},
		{"charger off grid", GrowthEffect{Type: EffectAddCharger, Entities: []json.RawMessage{raw(ChargerConfig{ID: "ch1", Pos: Position{99, 99}, Capacity: 1})}}, `charger "ch1"`},
		{"bad depot", GrowthEffect{Type: EffectAddDepot, Entities: []json.RawMessage{json.RawMessage("{")}}, "bad depot"},
		{"depot off grid", GrowthEffect{Type: EffectAddDepot, Entities: []json.RawMessage{raw(DepotConfig{ID: "d2", Pos: Position{99, 99}})}}, `depot "d2"`},
		{"unknown depot", GrowthEffect{Type: EffectUpgradeDepot, DepotID: "nope"}, "no depot with id"},
		{"unknown effect", GrowthEffect{Type: "mystery"}, "unknown effect type"},
	}
	for _, tc := range cases {
		city := tinyWorld(t).City
		err := city.applyEffect(tc.effect)
		if err == nil {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestAddChargerAndDepotEffectsWriteTerrain: the entity list alone is display
// metadata; charging and parking read the terrain, so the effect must write the
// charger/depot tile as well as append the entity.
func TestAddChargerAndDepotEffectsWriteTerrain(t *testing.T) {
	city := tinyWorld(t).City
	charger := ChargerConfig{ID: "ch1", Pos: Position{3, 3}, Capacity: 1}
	depot := DepotConfig{ID: "d2", Pos: Position{5, 5}}

	raw := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := city.applyEffect(GrowthEffect{Type: EffectAddCharger, Entities: []json.RawMessage{raw(charger)}}); err != nil {
		t.Fatalf("add charger: %v", err)
	}
	if !city.ChargingAt(charger.Pos) {
		t.Fatal("the charger's tile is not charging terrain")
	}
	if id := city.Chargers[len(city.Chargers)-1].ID; id != "ch1" {
		t.Fatalf("charger id = %q, want ch1", id)
	}

	if err := city.applyEffect(GrowthEffect{Type: EffectAddDepot, Entities: []json.RawMessage{raw(depot)}}); err != nil {
		t.Fatalf("add depot: %v", err)
	}
	if got, _ := city.At(depot.Pos); got != DepotTile {
		t.Fatalf("depot tile = %q, want %q", got, DepotTile)
	}
	if id := city.Depots[len(city.Depots)-1].ID; id != "d2" {
		t.Fatalf("depot id = %q, want d2", id)
	}
}

// TestUpgradeDepotEffect: the upgrade replaces only the positive knobs, so an
// omitted charge slot count keeps the authored number.
func TestUpgradeDepotEffect(t *testing.T) {
	city := tinyWorld(t).City
	before := city.Depots[0]

	if err := city.applyEffect(GrowthEffect{Type: EffectUpgradeDepot, DepotID: before.ID, Charge: 2}); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got := city.Depots[0]; got.ChargeSlots != 2 {
		t.Fatalf("upgraded depot = %+v, want 2 charge slots", got)
	}

	if err := city.applyEffect(GrowthEffect{Type: EffectUpgradeDepot, DepotID: before.ID}); err != nil {
		t.Fatalf("zero upgrade: %v", err)
	}
	if got := city.Depots[0]; got.ChargeSlots != 2 {
		t.Fatalf("zero upgrade changed the depot: %+v", got)
	}
}

// TestGrantEffectsAreNoOpsInTheCity: the economic effects are carried in the
// stage script but must not touch the grid; the world applies them.
func TestGrantEffectsAreNoOpsInTheCity(t *testing.T) {
	city := tinyWorld(t).City
	before, _ := city.At(Position{2, 2})
	for _, effect := range []GrowthEffect{
		{Type: EffectGrantMoney, Amount: 500},
		{Type: EffectGrantRep, Rep: 0.5},
		{Type: EffectGrantCarClass, Class: "van"},
	} {
		if err := city.applyEffect(effect); err != nil {
			t.Fatalf("%s: %v", effect.Type, err)
		}
	}
	if got, _ := city.At(Position{2, 2}); got != before {
		t.Fatalf("grant effect rewrote terrain: %q -> %q", before, got)
	}
}

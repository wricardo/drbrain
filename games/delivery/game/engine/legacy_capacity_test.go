package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyDepotCapacityIsAcceptedAndDropped(t *testing.T) {
	var depot DepotConfig
	raw := []byte(`{"id":"home","pos":{"x":2,"y":2},"capacity":4,"charge_slots":2}`)
	if err := json.Unmarshal(raw, &depot); err != nil {
		t.Fatalf("legacy depot capacity should decode: %v", err)
	}
	if depot.ID != "home" || depot.ChargeSlots != 2 {
		t.Fatalf("depot = %+v, want id home and 2 charge slots", depot)
	}
	out, err := json.Marshal(depot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "capacity") {
		t.Fatalf("re-encoded depot still carries legacy capacity: %s", out)
	}

	var effect GrowthEffect
	rawEffect := []byte(`{"type":"upgrade_depot","depot":"home","capacity":8,"charge_slots":8}`)
	if err := json.Unmarshal(rawEffect, &effect); err != nil {
		t.Fatalf("legacy upgrade capacity should decode: %v", err)
	}
	if effect.Type != EffectUpgradeDepot || effect.DepotID != "home" || effect.Charge != 8 {
		t.Fatalf("effect = %+v, want upgrade home with 8 charge slots", effect)
	}
}

// The compatibility shim must not weaken strict decoding for anything else.
func TestLegacyCapacityShimStillRejectsUnknownFields(t *testing.T) {
	var depot DepotConfig
	if err := json.Unmarshal([]byte(`{"id":"home","pos":{"x":2,"y":2},"bogus":1}`), &depot); err == nil {
		t.Fatal("depot unknown field was accepted")
	}

	var effect GrowthEffect
	if err := json.Unmarshal([]byte(`{"type":"upgrade_depot","depot":"home","bogus":1}`), &effect); err == nil {
		t.Fatal("growth effect unknown field was accepted")
	}
}

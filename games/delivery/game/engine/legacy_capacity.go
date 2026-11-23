package engine

import (
	"bytes"
	"encoding/json"
)

// Depot capacity was legacy map data: depots have always accepted any number of
// cars, but older city documents still carry the key. Accept and discard it so
// those maps keep loading after the field is gone; every other unknown field is
// still rejected, as DecodeCityConfig promises.
func (d *DepotConfig) UnmarshalJSON(data []byte) error {
	type depot DepotConfig
	aux := struct {
		*depot
		Capacity json.RawMessage `json:"capacity,omitempty"`
	}{depot: (*depot)(d)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(&aux)
}

// upgrade_depot likewise used to carry a capacity. It no longer means anything
// because parking is unlimited, so only charge_slots remains.
func (e *GrowthEffect) UnmarshalJSON(data []byte) error {
	type effect GrowthEffect
	aux := struct {
		*effect
		Capacity json.RawMessage `json:"capacity,omitempty"`
	}{effect: (*effect)(e)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(&aux)
}

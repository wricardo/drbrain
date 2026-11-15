package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadCityConfig reads and decodes a city JSON file. Unknown fields are an
// error: a typo in a knob name must not silently fall back to a zero value,
// because most of these knobs are pacing multipliers where zero is harmful.
func LoadCityConfig(path string) (*CityConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read city file: %w", err)
	}
	var cfg CityConfig
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills knobs that have a sensible non-zero default so a new
// city file does not have to restate them.
func (cfg *CityConfig) applyDefaults() error {
	if cfg.TickMS <= 0 {
		cfg.TickMS = DefaultTickMS
	}
	if cfg.GridSize > 0 && len(cfg.Layout) > 0 {
		// Legend is required; a missing entry is caught during parsing with a
		// precise row/col message, which is more useful than a generic error.
		if len(cfg.Legend) == 0 {
			return fmt.Errorf("legend is empty; every layout char needs a terrain")
		}
	}
	o := &cfg.Orders
	if o.MaxActiveOrders <= 0 {
		o.MaxActiveOrders = 6
	}
	if o.TargetOrdersPerCar <= 0 {
		o.TargetOrdersPerCar = 1.0
	}
	if o.MinSpawnGapTicks <= 0 {
		o.MinSpawnGapTicks = 12
	}
	if o.DeadlineSlack <= 0 {
		o.DeadlineSlack = 2.0
	}
	if o.LatePayMultiplier <= 0 {
		o.LatePayMultiplier = 0.6
	}
	if o.DistanceBonusPerTile == 0 {
		o.DistanceBonusPerTile = 3
	}
	if o.ReputationPerDelivery <= 0 {
		o.ReputationPerDelivery = 0.02
	}
	if o.ReputationPerLate <= 0 {
		o.ReputationPerLate = 0.05
	}
	if o.ExpirePenaltyReputation <= 0 {
		o.ExpirePenaltyReputation = 0.05
	}
	if cfg.Charging.DepotRate <= 0 {
		cfg.Charging.DepotRate = DefaultDepotChargeRate
	}
	if cfg.Charging.ChargerRate <= 0 {
		cfg.Charging.ChargerRate = DefaultChargerChargeRate
	}
	return nil
}

// DefaultTickMS is the fallback simulation step when a city omits tick_ms.
const DefaultTickMS = 300

// CityFromFile loads a city config and builds the initial City from it.
func CityFromFile(path string) (*City, *CityConfig, error) {
	cfg, err := LoadCityConfig(path)
	if err != nil {
		return nil, nil, err
	}
	city, err := NewCity(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("city %q: %w", cfg.Name, err)
	}
	applyInitialHiding(city, cfg)
	return city, cfg, nil
}

// applyInitialHiding hides every tile that belongs to a hidden district.
//
// District membership is a set of inclusive rectangles in the map file, so the
// layout stays a plain grid of legend chars and districts are described
// declaratively rather than by overloading terrain characters.
func applyInitialHiding(city *City, cfg *CityConfig) {
	for _, d := range cfg.Districts {
		if !d.Hidden {
			continue
		}
		for _, r := range d.Rects {
			city.HideRect(r[0], r[1], r[2], r[3])
		}
	}
}

package engine

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The byte decoder is what the city catalogue uses for PostgreSQL rows and
// builtin files alike; it must behave exactly like the path loader.
func TestDecodeCityConfigMatchesFileLoad(t *testing.T) {
	path := repoCityPath(t)
	fromFile, err := LoadCityConfig(path)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := DecodeCityConfig(raw)
	if err != nil {
		t.Fatalf("decode bytes: %v", err)
	}
	if !reflect.DeepEqual(fromBytes, fromFile) {
		t.Fatal("byte decode and file load disagree")
	}
}

func TestDecodeCityConfigRejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"name":"x","grid_size":5,"layout":["....."],"legend":{".":"road"},"bogus_knob":1}`)
	_, err := DecodeCityConfig(raw)
	if err == nil {
		t.Fatal("unknown field was accepted")
	}
	if !strings.Contains(err.Error(), "bogus_knob") {
		t.Fatalf("error %q does not name the offending field", err)
	}
}

func TestDecodeCityConfigRejectsMissingKnobs(t *testing.T) {
	// A map that states only geography must fail: every pacing knob is
	// required, and the error must name them rather than silently zeroing.
	raw := []byte(`{
		"name":"tiny",
		"grid_size":5,
		"layout":[".....",".....",".....",".....","....."],
		"legend":{".":"road"}
	}`)
	_, err := DecodeCityConfig(raw)
	if err == nil {
		t.Fatal("missing required knobs were accepted")
	}
	for _, want := range []string{"tick_ms", "orders.ramp_ticks", "fleet block is required", "closures block is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	cfg := completeConfig(minimalCityConfig())
	cfg.TickMS = 0
	cfg.Orders.LatePayMultiplier = 2
	cfg.Charging.DepotRate = 0
	cfg.CarClasses = map[string]CarClassConfig{
		"scooter": {StepCost: 0},
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	for _, want := range []string{
		"tick_ms", "orders.late_pay_multiplier", "charging.depot_rate",
		"car_classes.scooter.capacity", "car_classes.scooter.max_battery",
		"car_classes.scooter.step_cost",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestDraftMayOmitRewardPricing(t *testing.T) {
	cfg := completeConfig(minimalCityConfig())
	cfg.Items = map[string]string{"pizza": "Pizza"}
	cfg.Orders.RewardBase = nil

	if err := ValidateCityConfigStructural(cfg); err != nil {
		t.Fatalf("draft with an unpriced item was refused: %v", err)
	}
	err := ValidateCityConfig(cfg)
	if err == nil {
		t.Fatal("publish accepted an item with no reward_base")
	}
	if !strings.Contains(err.Error(), "reward_base.pizza") {
		t.Fatalf("error %q does not name the unpriced item", err)
	}
}

// A draft may be structurally sound while not yet playable: it is missing its
// depot. Structural validation is the save gate; the publish/startup validator
// must still refuse it.
func TestValidationSplitAcceptsNonWinnableDraft(t *testing.T) {
	cfg := minimalCityConfig()
	cfg.Depots = nil

	if err := ValidateCityConfigStructural(cfg); err != nil {
		t.Fatalf("structural validation refused a draft: %v", err)
	}
	if err := ValidateCityConfig(cfg); err == nil {
		t.Fatal("full validation accepted a city with no depot")
	}
}

func TestValidationSplitAcceptsPlayableCity(t *testing.T) {
	cfg := minimalCityConfig()
	if err := ValidateCityConfigStructural(cfg); err != nil {
		t.Fatalf("structural validation: %v", err)
	}
	if err := ValidateCityConfig(cfg); err != nil {
		t.Fatalf("full validation: %v", err)
	}
	city, err := CityFromConfig(cfg)
	if err != nil {
		t.Fatalf("build city: %v", err)
	}
	if city.Name != cfg.Name || len(city.Depots) != 1 {
		t.Fatalf("built city = %+v", city)
	}
}

// minimalCityConfig is a 5x5 city with one depot: the smallest config that
// passes the full validator. It is unrelated to the shipped map on purpose, so
// these tests do not move when the generated city changes.
func minimalCityConfig() *CityConfig {
	return completeConfig(&CityConfig{
		Name:     "Tiny",
		GridSize: 5,
		Layout: []string{
			".....",
			"#####",
			"##D##",
			"#####",
			".....",
		},
		Legend: map[string]string{
			".": "road",
			"#": "building",
			"D": "depot",
		},
		Depots: []DepotConfig{
			{ID: "home", Pos: Position{X: 2, Y: 2}},
		},
	})
}

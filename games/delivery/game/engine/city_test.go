package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// repoCityPath returns the path to the shipped city file from the test's cwd.
func repoCityPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatalf("resolve city path: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("city file missing at %s: %v", p, err)
	}
	return p
}

func loadRepoCity(t *testing.T) (*City, *CityConfig) {
	t.Helper()
	city, cfg, err := CityFromFile(repoCityPath(t))
	if err != nil {
		t.Fatalf("load city: %v", err)
	}
	return city, cfg
}

func TestLoadShippedCity(t *testing.T) {
	city, cfg := loadRepoCity(t)

	// The map is generated (scripts/genmap.py), so its size is not pinned to
	// a literal here: what matters is that the file, the config and the
	// loaded city agree, and that the shape is still the one the game is
	// balanced around (a street grid with building blocks).
	if city.GridSize != cfg.GridSize {
		t.Errorf("grid size = %d, want %d from the config", city.GridSize, cfg.GridSize)
	}
	if cfg.GridSize < 20 {
		t.Errorf("grid size = %d, want at least 20: a smaller city has no room for a street grid",
			cfg.GridSize)
	}
	if got, want := len(city.Kitchens), len(cfg.Kitchens); got != want {
		t.Errorf("kitchens = %d, want %d", got, want)
	}
	if len(city.Depots) == 0 {
		t.Fatal("no depots loaded")
	}
	if city.TickMS != 600 {
		t.Errorf("tick_ms = %d, want 600 (1x is 600 ms per tick)", city.TickMS)
	}
}

// TestShippedCityIsAStreetGrid pins the thing the Manhattan layout exists for:
// real buildings, real intersections, and routes that are not a plaza.
//
// The previous map had zero building tiles and 62% of its walkable tiles with
// four open neighbours, which is why a road closure there cost a median of zero
// extra steps. This test fails if the map drifts back to open space.
func TestShippedCityIsAStreetGrid(t *testing.T) {
	city, _ := loadRepoCity(t)
	city.RevealAll()

	buildings := 0
	walkable := 0
	for y := range city.GridSize {
		for x := range city.GridSize {
			p := Position{x, y}
			if !city.Walkable(p) {
				if t, _ := city.At(p); t == BuildingTile {
					buildings++
				}
				continue
			}
			walkable++
		}
	}
	if buildings < 100 {
		t.Errorf("only %d building tiles: the city has no blocks to route around", buildings)
	}
	if walkable < 150 {
		t.Errorf("only %d walkable tiles: too small to be a city", walkable)
	}

	// A street grid means a meaningful share of walkable tiles are corridors
	// (two neighbours) rather than open intersections. Manhattan-style blocks
	// land around 78% corridors at street period 3.
	corridors := 0
	for y := range city.GridSize {
		for x := range city.GridSize {
			p := Position{x, y}
			if !city.Walkable(p) {
				continue
			}
			open := 0
			for _, d := range AllDirections {
				if n, ok := p.Neighbour(d); ok && city.Walkable(n) {
					open++
				}
			}
			if open <= 2 {
				corridors++
			}
		}
	}
	share := 100 * corridors / walkable
	if share < 55 {
		t.Errorf("only %d%% of walkable tiles are corridors: this is a plaza, not a grid "+
			"(closures cannot reroute traffic here)", share)
	}
}

func TestShippedCityHidesOuterDistricts(t *testing.T) {
	city, cfg := loadRepoCity(t)

	// The harbour district is authored hidden: its tiles must start
	// unexplored, and downtown's must not. Both are read from the config
	// rather than hardcoded, so a regenerated map cannot silently pass.
	harbour, ok := cfg.Districts["harbour"]
	if !ok || len(harbour.Rects) == 0 {
		t.Fatal("shipped city has no harbour district")
	}
	r := harbour.Rects[0]
	if !city.IsHidden(Position{r[0], r[1]}) {
		t.Error("harbour district should start hidden")
	}
	if city.HiddenCount() == 0 {
		t.Error("expected some hidden tiles at start")
	}
	// The depot's own tile is always known terrain.
	if city.IsHidden(city.Depots[0].Pos) {
		t.Error("depot tile should be visible at start")
	}
}

func TestShippedCityStrandsNothing(t *testing.T) {
	city, _ := loadRepoCity(t)

	// Hidden districts make their entities unreachable by design, so this
	// assertion runs on the revealed city: the geography itself is coherent.
	city.RevealAll()
	if stranded := city.StrandedEntities(); len(stranded) != 0 {
		t.Fatalf("revealed city strands entities: %v", stranded)
	}
}

func TestHiddenTilesAreImpassable(t *testing.T) {
	city, _ := loadRepoCity(t)

	// Pick any hidden tile rather than a literal: the district geometry is
	// generated, and what this test is about is the fog rule, not a coordinate.
	var hidden Position
	found := false
	for y := range city.GridSize {
		if found {
			break
		}
		for x := range city.GridSize {
			if city.IsHidden(Position{x, y}) {
				hidden = Position{x, y}
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no hidden tiles in the shipped city")
	}
	if city.Walkable(hidden) {
		t.Fatalf("hidden tile %v should not be walkable", hidden)
	}
	city.RevealTile(hidden)
	if !city.Walkable(hidden) {
		t.Fatalf("revealed tile %v should be walkable", hidden)
	}
}

func TestEastBridgeConnectsHarbour(t *testing.T) {
	city, cfg := loadRepoCity(t)

	// Find the bridge and gate stages from the schedule.
	bridge, ok := growthByIDFromConfig(cfg, "g02_harbour_gate")
	if !ok {
		t.Fatal("shipped city has no East Bridge growth stage")
	}
	gate, ok := growthByIDFromConfig(cfg, "g02_harbour_gate")
	if !ok {
		t.Fatal("shipped city has no Harbour Gate growth stage")
	}

	depot := city.Depots[0].Pos
	var harbourKitchen Position
	for _, k := range city.Kitchens {
		if k.ID == "k_sushi" {
			harbourKitchen = k.Pos
		}
	}
	if harbourKitchen == (Position{}) {
		t.Fatal("shipped city has no harbour kitchen")
	}

	// Reveal the harbour but do NOT bridge it: the canal must still block it.
	for _, e := range gate.Effects {
		if e.Type == EffectRevealDistrict {
			city.RevealRegion(city.DistrictTiles(e.District))
		}
	}
	if d := city.Distance(depot, harbourKitchen); d >= 0 {
		t.Fatalf("harbour should be unreachable before the bridge, got distance %d", d)
	}

	if err := city.applyEffects(bridge.Effects); err != nil {
		t.Fatalf("apply bridge: %v", err)
	}
	if d := city.Distance(depot, harbourKitchen); d < 0 {
		t.Fatal("harbour still unreachable after building the east bridge")
	}
}

// growthByIDFromConfig looks a growth stage up in the city config.
func growthByIDFromConfig(cfg *CityConfig, id string) (GrowthConfig, bool) {
	for _, g := range cfg.Growth {
		if g.ID == id {
			return g, true
		}
	}
	return GrowthConfig{}, false
}

func TestGrowthValidationRejectsOffGridEffects(t *testing.T) {
	city, cfg := loadRepoCity(t)

	// A stage that writes off-grid must be rejected before the session starts,
	// naming the stage rather than half-applying it twenty minutes in.
	cfg.Growth = append(cfg.Growth, GrowthConfig{
		ID: "g_bad", Tick: 99_999,
		Effects: []GrowthEffect{{
			Type: EffectBuildBridge,
			Tiles: []TileWrite{
				{X: 5, Y: 5, Terrain: "road"},
				{X: 999, Y: 999, Terrain: "road"},
			},
		}},
	})
	if _, err := NewWorldSeeded(city, cfg, 1); err == nil {
		t.Fatal("expected off-grid growth stage to fail validation")
	}
}

func TestGrowthValidationRejectsStranding(t *testing.T) {
	city, cfg := loadRepoCity(t)

	// Wall a kitchen off completely with water: the only way in is from the
	// four neighbours of the kitchen tile. Derived from the map so the test
	// keeps testing the guard rather than a coordinate.
	kitchen := city.Kitchens[0].Pos
	var walls []TileWrite
	for _, d := range AllDirections {
		if n, ok := kitchen.Neighbour(d); ok {
			walls = append(walls, TileWrite{X: n.X, Y: n.Y, Terrain: "water"})
		}
	}
	cfg.Growth = append(cfg.Growth, GrowthConfig{
		ID: "g_wall", Tick: 99_999,
		Effects: []GrowthEffect{{Type: EffectBuildBridge, Tiles: walls}},
	})
	if _, err := NewWorldSeeded(city, cfg, 1); err == nil {
		t.Fatal("expected the stranding guard to reject a growth stage that walls off the kitchen")
	}
}

func TestWalkCostChargesExtraOnGround(t *testing.T) {
	city, cfg := loadRepoCity(t)
	class := cfg.CarClasses["scooter"]
	city.RevealAll()

	// Locate one road tile and one ground tile on the map rather than naming
	// coordinates: the ground tiles are authored into the generated layout.
	var road, ground Position
	haveRoad, haveGround := false, false
	for y := range city.GridSize {
		for x := range city.GridSize {
			t, _ := city.At(Position{x, y})
			switch t {
			case RoadTile:
				if !haveRoad {
					road, haveRoad = Position{x, y}, true
				}
			case GroundTile:
				if !haveGround {
					ground, haveGround = Position{x, y}, true
				}
			}
		}
	}
	if !haveRoad || !haveGround {
		t.Fatalf("map needs at least one road and one ground tile (road=%v ground=%v)",
			haveRoad, haveGround)
	}
	if got := city.WalkCost(road, class); got != 1 {
		t.Errorf("road walk cost = %d, want 1", got)
	}
	if got := city.WalkCost(ground, class); got != 2 {
		t.Errorf("ground walk cost = %d, want 2", got)
	}
}

func TestValidateRejectsMismatchedLayoutRow(t *testing.T) {
	cfg := &CityConfig{
		Name:     "broken",
		GridSize: 5,
		Layout: []string{
			"WWWWW", "WRRRW", "WRRRW", "WRRR", "WWWWW",
		},
		Legend: map[string]string{"W": "water", "R": "road"},
		Depots: []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
	}
	if _, err := NewCity(cfg); err == nil {
		t.Fatal("expected short layout row to be rejected")
	}
}

func TestValidateRejectsUnknownLegendChar(t *testing.T) {
	cfg := &CityConfig{
		Name:     "broken",
		GridSize: 5,
		Layout:   []string{"WWWWW", "WRZRW", "WRRRW", "WRRRW", "WWWWW"},
		Legend:   map[string]string{"W": "water", "R": "road"},
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
	}
	if _, err := NewCity(cfg); err == nil {
		t.Fatal("expected unknown legend char to be rejected")
	}
}

func TestValidateRejectsEntityOnWrongTerrain(t *testing.T) {
	cfg := &CityConfig{
		Name:     "broken",
		GridSize: 5,
		Layout:   []string{"WWWWW", "WRRRW", "WRRRW", "WRRRW", "WWWWW"},
		Legend:   map[string]string{"W": "water", "R": "road"},
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}}},
		Items:    map[string]string{"pizza": "Pizza"},
		// Kitchen placed on road, not on a kitchen tile.
		Kitchens: []KitchenConfig{
			{ID: "k", Pos: Position{2, 2}, Menu: []string{"pizza"}},
		},
	}
	if _, err := NewCity(cfg); err == nil {
		t.Fatal("expected kitchen-on-road to be rejected")
	}
}

func TestParseDirectionAliases(t *testing.T) {
	cases := map[string]Direction{
		"north": North, "up": North,
		"south": South, "down": South,
		"east": East, "right": East,
		"west": West, "left": West,
	}
	for in, want := range cases {
		got, ok := ParseDirection(in)
		if !ok || got != want {
			t.Errorf("ParseDirection(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := ParseDirection("northwest"); ok {
		t.Error("expected unknown direction to be rejected")
	}
}

func TestNeighbourStaysInBoundsByArithmetic(t *testing.T) {
	// Neighbour does not clamp; it reports the arithmetic result and lets
	// City.At reject off-grid. This pins that contract.
	p := Position{0, 0}
	n, ok := p.Neighbour(North)
	if !ok {
		t.Fatal("north should be a valid direction")
	}
	if n != (Position{0, -1}) {
		t.Errorf("north of (0,0) = %v, want (0,-1)", n)
	}
}

func TestHiddenTilesHideEntities(t *testing.T) {
	city, _ := loadRepoCity(t)

	// Fog that leaks entity positions is decorative, not a gate. Find an
	// entity that starts hidden and assert it renders as '?' until revealed.
	var hiddenEnt Position
	var wantGlyph byte
	found := false
	for _, k := range city.Kitchens {
		if city.IsHidden(k.Pos) {
			hiddenEnt, wantGlyph, found = k.Pos, 'K', true
			break
		}
	}
	if !found {
		for _, ch := range city.Chargers {
			if city.IsHidden(ch.Pos) {
				hiddenEnt, wantGlyph, found = ch.Pos, 'C', true
				break
			}
		}
	}
	if !found {
		t.Fatal("no entity starts in a hidden district; the map no longer gates content")
	}

	rows := city.RenderRows(nil)
	if got := rows[hiddenEnt.Y][hiddenEnt.X]; got != '?' {
		t.Fatalf("hidden tile rendered %q, want '?' (entity leaked through fog)", string(got))
	}

	// No entity glyph may appear anywhere inside a hidden region.
	for y := range city.GridSize {
		for x := range city.GridSize {
			if !city.IsHidden(Position{x, y}) {
				continue
			}
			if ch := rows[y][x]; ch != '?' {
				t.Fatalf("hidden tile (%d,%d) rendered %q", x, y, string(ch))
			}
		}
	}

	// After revealing its district, the entity becomes visible as it should.
	city.RevealAll()
	rows = city.RenderRows(nil)
	if got := rows[hiddenEnt.Y][hiddenEnt.X]; got != wantGlyph {
		t.Fatalf("revealed entity rendered %q, want %q", string(got), string(wantGlyph))
	}
}

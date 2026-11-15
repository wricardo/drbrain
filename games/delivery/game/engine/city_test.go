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

	if city.GridSize != 20 {
		t.Errorf("grid size = %d, want 20", city.GridSize)
	}
	if got, want := len(city.Kitchens), len(cfg.Kitchens); got != want {
		t.Errorf("kitchens = %d, want %d", got, want)
	}
	if len(city.Depots) == 0 {
		t.Fatal("no depots loaded")
	}
	if city.TickMS != 300 {
		t.Errorf("tick_ms = %d, want 300", city.TickMS)
	}
}

func TestShippedCityHidesOuterDistricts(t *testing.T) {
	city, _ := loadRepoCity(t)

	// The harbour is authored hidden: its tiles must start unexplored.
	if !city.IsHidden(Position{17, 3}) {
		t.Error("harbour charger tile should start hidden")
	}
	// Downtown is visible.
	if city.IsHidden(Position{2, 2}) {
		t.Error("depot tile should be visible at start")
	}
	if city.HiddenCount() == 0 {
		t.Error("expected some hidden tiles at start")
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

	harbour := Position{17, 3}
	if city.Walkable(harbour) {
		t.Fatal("hidden harbour tile should not be walkable")
	}
	city.RevealTile(harbour)
	if !city.Walkable(harbour) {
		t.Fatal("revealed charger tile should be walkable")
	}
}

func TestEastBridgeConnectsHarbour(t *testing.T) {
	city, _ := loadRepoCity(t)
	city.RevealAll()

	depot := city.Depots[0].Pos
	harbour := Position{17, 3}

	if d := city.Distance(depot, harbour); d >= 0 {
		t.Fatalf("harbour should be unreachable before the bridge, got distance %d", d)
	}

	build := BuildConfig{
		ID:   "b02_east_bridge",
		Cost: 300,
		Effects: []BuildEffect{{
			Type: EffectBuildBridge,
			Tiles: []TileWrite{
				{X: 12, Y: 2, Terrain: "road"},
				{X: 13, Y: 2, Terrain: "road"},
			},
		}},
	}
	if _, err := city.ApplyBuild(build); err != nil {
		t.Fatalf("apply bridge: %v", err)
	}

	if d := city.Distance(depot, harbour); d < 0 {
		t.Fatal("harbour still unreachable after building the east bridge")
	}
}

func TestApplyBuildIsAtomicOnFailure(t *testing.T) {
	city, _ := loadRepoCity(t)
	city.RevealAll()

	before := city.RenderASCII(nil)

	// A build that writes off-grid must not partially apply.
	bad := BuildConfig{
		ID: "b_bad",
		Effects: []BuildEffect{{
			Type: EffectBuildBridge,
			Tiles: []TileWrite{
				{X: 5, Y: 5, Terrain: "road"},
				{X: 999, Y: 999, Terrain: "road"},
			},
		}},
	}
	if _, err := city.ApplyBuild(bad); err == nil {
		t.Fatal("expected off-grid build to fail")
	}
	if after := city.RenderASCII(nil); after != before {
		t.Fatal("failed build mutated the city")
	}
}

func TestApplyBuildRejectsStranding(t *testing.T) {
	city, _ := loadRepoCity(t)
	city.RevealAll()

	// Wall the kitchen off completely with water: the only way in is from
	// the four neighbours of the kitchen tile.
	kitchen := Position{4, 2}
	walls := []TileWrite{
		{X: kitchen.X, Y: kitchen.Y - 1, Terrain: "water"},
		{X: kitchen.X, Y: kitchen.Y + 1, Terrain: "water"},
		{X: kitchen.X - 1, Y: kitchen.Y, Terrain: "water"},
		{X: kitchen.X + 1, Y: kitchen.Y, Terrain: "water"},
	}
	_, err := city.ApplyBuild(BuildConfig{
		ID:      "b_wall",
		Effects: []BuildEffect{{Type: EffectBuildBridge, Tiles: walls}},
	})
	if err == nil {
		t.Fatal("expected the stranding guard to reject walling off the kitchen")
	}
}

func TestWalkCostChargesExtraOnGround(t *testing.T) {
	city, cfg := loadRepoCity(t)
	class := cfg.CarClasses["scooter"]

	road := Position{3, 3}
	ground := Position{1, 8}
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
		Depots: []DepotConfig{{ID: "d", Pos: Position{1, 1}, Capacity: 1}},
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
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}, Capacity: 1}},
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
		Depots:   []DepotConfig{{ID: "d", Pos: Position{1, 1}, Capacity: 1}},
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

	// The harbour charger sits in a district that starts hidden. Rendering
	// must not reveal it: fog that leaks entity positions is decorative, not a
	// gate.
	charger := Position{17, 3}
	if !city.IsHidden(charger) {
		t.Fatal("test precondition: harbour charger should start hidden")
	}

	rows := city.RenderRows(nil)
	if got := rows[charger.Y][charger.X]; got != '?' {
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

	// After revealing, the charger becomes visible as it should.
	city.RevealRect(14, 1, 18, 8)
	rows = city.RenderRows(nil)
	if got := rows[charger.Y][charger.X]; got != 'C' {
		t.Fatalf("revealed charger rendered %q, want 'C'", string(got))
	}
}

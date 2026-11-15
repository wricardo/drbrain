package engine

import (
	"fmt"
	"sort"
	"strings"
)

// City is the terrain and static geography of a session: the grid plus the
// entities that sit on it. It is derived from a CityConfig and then mutated
// only by builds (which reveal districts and rewrite tiles).
type City struct {
	Name     string
	GridSize int
	TickMS   int

	// Grid is row-major: Grid[y][x]. Always GridSize x GridSize.
	Grid [][]CellType

	// Hidden reports, per coordinate, whether the tile is unexplored. A
	// hidden tile is impassable and renders as unknown; builds clear the flag
	// to reveal a district.
	Hidden [][]bool

	Items     map[string]string
	Kitchens  []Kitchen
	Customers []Customer
	Depots    []Depot
	Chargers  []Charger
	Districts map[string]District
}

// District is a named region used by reveal-style builds.
type District struct {
	Name        string
	Description string
	Rects       [][4]int
}

// Kitchen is a pickup point.
type Kitchen struct {
	ID   string
	Name string
	Pos  Position
	Menu []string
}

// Serves reports whether this kitchen offers the given item.
func (k Kitchen) Serves(item string) bool {
	for _, m := range k.Menu {
		if m == item {
			return true
		}
	}
	return false
}

// Customer is a dropoff point that raises orders.
type Customer struct {
	ID                 string
	Name               string
	Pos                Position
	Demands            []string
	OrderIntervalTicks int
	JitterTicks        int
}

// Depot is a base that parks and recharges cars.
type Depot struct {
	ID          string
	Pos         Position
	Capacity    int
	ChargeSlots int
}

// Charger is a standalone charging point.
type Charger struct {
	ID       string
	Pos      Position
	Capacity int
}

// At returns the terrain at a coordinate, and false if it is off the grid.
// Off-grid is treated as impassable, which keeps callers free of bounds noise.
func (c *City) At(p Position) (CellType, bool) {
	if p.Y < 0 || p.Y >= c.GridSize || p.X < 0 || p.X >= c.GridSize {
		return "", false
	}
	return c.Grid[p.Y][p.X], true
}

// IsHidden reports whether a coordinate is still unexplored. Off-grid counts
// as hidden so it is never revealed by accident.
func (c *City) IsHidden(p Position) bool {
	if p.Y < 0 || p.Y >= c.GridSize || p.X < 0 || p.X >= c.GridSize {
		return true
	}
	return c.Hidden[p.Y][p.X]
}

// WalkCost is the battery a car of the given class spends stepping onto p.
// Ground costs an extra point: that is what makes paving it a real upgrade
// rather than decoration (IDEA.txt O3).
func (c *City) WalkCost(p Position, class CarClassConfig) int {
	base := class.StepCost
	if base <= 0 {
		base = 1
	}
	t, ok := c.At(p)
	if !ok {
		return base
	}
	if t == GroundTile {
		return base + 1
	}
	return base
}

// Walkable reports whether a car may enter p right now. Hidden and impassable
// terrain both block.
func (c *City) Walkable(p Position) bool {
	if c.IsHidden(p) {
		return false
	}
	t, ok := c.At(p)
	if !ok {
		return false
	}
	return t.Passable()
}

// KitchenAt returns the kitchen occupying a coordinate, if any.
func (c *City) KitchenAt(p Position) (Kitchen, bool) {
	for _, k := range c.Kitchens {
		if k.Pos == p {
			return k, true
		}
	}
	return Kitchen{}, false
}

// CustomerAt returns the customer occupying a coordinate, if any.
func (c *City) CustomerAt(p Position) (Customer, bool) {
	for _, cu := range c.Customers {
		if cu.Pos == p {
			return cu, true
		}
	}
	return Customer{}, false
}

// ChargingAt reports whether a car standing on p can recharge, i.e. it is a
// depot or a charger tile.
func (c *City) ChargingAt(p Position) bool {
	t, ok := c.At(p)
	if !ok {
		return false
	}
	return t.Charges()
}

// NewCity builds a City from a config, validating as it goes. It returns the
// first structural problem rather than accumulating them, because a map that
// fails validation is not partially usable.
func NewCity(cfg *CityConfig) (*City, error) {
	c, err := parseCity(cfg)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// parseCity builds the City without semantic validation. Validation is split
// out so tests can construct deliberately broken cities and assert on the
// error that Validate produces.
func parseCity(cfg *CityConfig) (*City, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil city config")
	}
	if cfg.GridSize < MinGridSize || cfg.GridSize > MaxGridSize {
		return nil, fmt.Errorf("grid_size %d out of range [%d,%d]",
			cfg.GridSize, MinGridSize, MaxGridSize)
	}
	if len(cfg.Layout) != cfg.GridSize {
		return nil, fmt.Errorf("layout has %d rows, want %d",
			len(cfg.Layout), cfg.GridSize)
	}

	c := &City{
		Name:      cfg.Name,
		GridSize:  cfg.GridSize,
		TickMS:    cfg.TickMS,
		Grid:      make([][]CellType, cfg.GridSize),
		Hidden:    make([][]bool, cfg.GridSize),
		Items:     map[string]string{},
		Districts: map[string]District{},
	}
	for k, v := range cfg.Items {
		c.Items[k] = v
	}
	for name, d := range cfg.Districts {
		c.Districts[name] = District{Name: name, Description: d.Description, Rects: d.Rects}
	}

	// Terrain. Legend lookup is by single character.
	for y, row := range cfg.Layout {
		if len(row) != cfg.GridSize {
			return nil, fmt.Errorf("layout row %d has %d columns, want %d: %q",
				y, len(row), cfg.GridSize, row)
		}
		c.Grid[y] = make([]CellType, cfg.GridSize)
		c.Hidden[y] = make([]bool, cfg.GridSize)
		for x := range cfg.GridSize {
			ch := string(row[x])
			name, ok := cfg.Legend[ch]
			if !ok {
				return nil, fmt.Errorf("layout row %d col %d: char %q not in legend", y, x, ch)
			}
			ct := CellType(name)
			if !ct.known() {
				return nil, fmt.Errorf("layout row %d col %d: legend %q maps to unknown terrain %q",
					y, x, ch, name)
			}
			c.Grid[y][x] = ct
		}
	}

	// Entities. Positions are validated in Validate so the errors read the
	// same whether they came from the layout or the entity table.
	for _, k := range cfg.Kitchens {
		c.Kitchens = append(c.Kitchens, Kitchen{
			ID: k.ID, Name: k.Name, Pos: k.Pos, Menu: append([]string(nil), k.Menu...),
		})
	}
	for _, cu := range cfg.Customers {
		c.Customers = append(c.Customers, Customer{
			ID: cu.ID, Name: cu.Name, Pos: cu.Pos,
			Demands:            append([]string(nil), cu.Demands...),
			OrderIntervalTicks: cu.OrderIntervalTicks,
			JitterTicks:        cu.JitterTicks,
		})
	}
	for _, d := range cfg.Depots {
		c.Depots = append(c.Depots, Depot{
			ID: d.ID, Pos: d.Pos, Capacity: d.Capacity, ChargeSlots: d.ChargeSlots,
		})
	}
	for _, ch := range cfg.Chargers {
		c.Chargers = append(c.Chargers, Charger{ID: ch.ID, Pos: ch.Pos, Capacity: ch.Capacity})
	}

	return c, nil
}

// known reports whether a terrain name is one the engine understands.
func (t CellType) known() bool {
	switch t {
	case WaterTile, BuildingTile, RoadTile, GroundTile, DepotTile, ChargerTile, KitchenTile, HouseTile:
		return true
	}
	return false
}

// Validate checks every cross-reference in the city and returns the first
// problem found. It deliberately does NOT prove the map is completable; it
// only guarantees the data is coherent enough to simulate.
func (c *City) Validate() error {
	seenKitchens := map[string]bool{}
	for _, k := range c.Kitchens {
		if k.ID == "" {
			return fmt.Errorf("kitchen with empty id")
		}
		if seenKitchens[k.ID] {
			return fmt.Errorf("duplicate kitchen id %q", k.ID)
		}
		seenKitchens[k.ID] = true
		if err := c.checkEntityTile(k.ID, k.Pos, KitchenTile, "kitchen"); err != nil {
			return err
		}
		if len(k.Menu) == 0 {
			return fmt.Errorf("kitchen %q serves nothing", k.ID)
		}
		for _, item := range k.Menu {
			if _, ok := c.Items[item]; !ok {
				return fmt.Errorf("kitchen %q serves unknown item %q", k.ID, item)
			}
		}
	}

	seenCustomers := map[string]bool{}
	for _, cu := range c.Customers {
		if cu.ID == "" {
			return fmt.Errorf("customer with empty id")
		}
		if seenCustomers[cu.ID] {
			return fmt.Errorf("duplicate customer id %q", cu.ID)
		}
		seenCustomers[cu.ID] = true
		if err := c.checkEntityTile(cu.ID, cu.Pos, HouseTile, "customer"); err != nil {
			return err
		}
		if len(cu.Demands) == 0 {
			return fmt.Errorf("customer %q demands nothing", cu.ID)
		}
		if cu.OrderIntervalTicks <= 0 {
			return fmt.Errorf("customer %q has non-positive order_interval_ticks", cu.ID)
		}
		for _, item := range cu.Demands {
			if _, ok := c.Items[item]; !ok {
				return fmt.Errorf("customer %q demands unknown item %q", cu.ID, item)
			}
			if !c.anyKitchenServes(item) {
				// Not fatal for simulation, but it is a content bug: no order
				// for this item could ever be fulfilled.
				return fmt.Errorf("customer %q demands %q but no kitchen serves it", cu.ID, item)
			}
		}
	}

	if len(c.Depots) == 0 {
		return fmt.Errorf("city has no depot; cars would have nowhere to start")
	}
	for i, d := range c.Depots {
		if err := c.checkEntityTile(d.ID, d.Pos, DepotTile, "depot"); err != nil {
			return err
		}
		if d.Capacity <= 0 {
			return fmt.Errorf("depot %q has non-positive capacity", d.ID)
		}
		if i > 0 && c.Depots[i-1].ID == d.ID {
			return fmt.Errorf("duplicate depot id %q", d.ID)
		}
	}

	for _, ch := range c.Chargers {
		if err := c.checkEntityTile(ch.ID, ch.Pos, ChargerTile, "charger"); err != nil {
			return err
		}
	}

	return nil
}

// anyKitchenServes reports whether some kitchen on the map offers item.
func (c *City) anyKitchenServes(item string) bool {
	for _, k := range c.Kitchens {
		if k.Serves(item) {
			return true
		}
	}
	return false
}

// checkEntityTile verifies an entity's coordinate is on the grid and has the
// terrain its legend entry promised.
func (c *City) checkEntityTile(id string, p Position, want CellType, kind string) error {
	t, ok := c.At(p)
	if !ok {
		return fmt.Errorf("%s %q at %s is off the %dx%d grid",
			kind, id, p, c.GridSize, c.GridSize)
	}
	if t != want {
		return fmt.Errorf("%s %q at %s sits on %q terrain, want %q",
			kind, id, p, t, want)
	}
	return nil
}

// RevealRegion clears the hidden flag for every tile of a district-shaped
// region described by a set of coordinates.
func (c *City) RevealRegion(ps []Position) int {
	n := 0
	for _, p := range ps {
		if c.RevealTile(p) {
			n++
		}
	}
	return n
}

// RevealTile reveals a single coordinate and reports whether it changed.
func (c *City) RevealTile(p Position) bool {
	if p.Y < 0 || p.Y >= c.GridSize || p.X < 0 || p.X >= c.GridSize {
		return false
	}
	if !c.Hidden[p.Y][p.X] {
		return false
	}
	c.Hidden[p.Y][p.X] = false
	return true
}

// RevealRect reveals an inclusive rectangle and returns how many tiles changed.
func (c *City) RevealRect(x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if c.RevealTile(Position{x, y}) {
				n++
			}
		}
	}
	return n
}

// HideRect marks an inclusive rectangle as unexplored.
func (c *City) HideRect(x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y <= y1; y++ {
		if y < 0 || y >= c.GridSize {
			continue
		}
		for x := x0; x <= x1; x++ {
			if x < 0 || x >= c.GridSize {
				continue
			}
			if !c.Hidden[y][x] {
				c.Hidden[y][x] = true
				n++
			}
		}
	}
	return n
}

// SetTerrain overwrites one tile's terrain, used by builds like a bridge.
// It reports an error for off-grid coordinates so a malformed build fails
// loudly instead of silently doing nothing.
func (c *City) SetTerrain(p Position, t CellType) error {
	if p.Y < 0 || p.Y >= c.GridSize || p.X < 0 || p.X >= c.GridSize {
		return fmt.Errorf("terrain write at %s is off the %dx%d grid", p, c.GridSize, c.GridSize)
	}
	if !t.known() {
		return fmt.Errorf("unknown terrain %q", t)
	}
	c.Grid[p.Y][p.X] = t
	return nil
}

// RevealAll clears every hidden flag. Used by survey-style builds and tests.
func (c *City) RevealAll() int {
	n := 0
	for y := range c.Hidden {
		for x := range c.Hidden[y] {
			if c.Hidden[y][x] {
				c.Hidden[y][x] = false
				n++
			}
		}
	}
	return n
}

// HiddenCount returns how many tiles are still unexplored.
func (c *City) HiddenCount() int {
	n := 0
	for y := range c.Hidden {
		for _, h := range c.Hidden[y] {
			if h {
				n++
			}
		}
	}
	return n
}

// Clone returns a deep copy, used to test a build's effects on a throwaway
// grid before committing them.
func (c *City) Clone() *City {
	out := *c
	out.Grid = make([][]CellType, len(c.Grid))
	for y := range c.Grid {
		out.Grid[y] = append([]CellType(nil), c.Grid[y]...)
	}
	out.Hidden = make([][]bool, len(c.Hidden))
	for y := range c.Hidden {
		out.Hidden[y] = append([]bool(nil), c.Hidden[y]...)
	}
	out.Items = make(map[string]string, len(c.Items))
	for k, v := range c.Items {
		out.Items[k] = v
	}
	out.Kitchens = append([]Kitchen(nil), c.Kitchens...)
	out.Customers = append([]Customer(nil), c.Customers...)
	out.Depots = append([]Depot(nil), c.Depots...)
	out.Chargers = append([]Charger(nil), c.Chargers...)
	out.Districts = make(map[string]District, len(c.Districts))
	for k, v := range c.Districts {
		out.Districts[k] = v
	}
	return &out
}

// RenderASCII draws the city as text, one line per row.
//
// Unexplored tiles draw as '?'. Entities override their terrain glyph so a
// kitchen is visibly a kitchen, not just road. When cars are supplied their
// positions are drawn as digits (1-9) on top of everything.
func (c *City) RenderASCII(cars []RenderCar) string {
	rows := c.RenderRows(cars)
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderRows is RenderASCII without the trailing newlines, which is the shape
// JSON clients and the web UI want.
func (c *City) RenderRows(cars []RenderCar) []string {
	glyph := map[CellType]byte{
		WaterTile: '#', BuildingTile: 'B', RoadTile: '.', GroundTile: ',',
		DepotTile: 'D', ChargerTile: 'C', KitchenTile: 'K', HouseTile: 'H',
	}

	// Entity positions win over their terrain glyph.
	overlay := map[Position]byte{}
	for _, k := range c.Kitchens {
		overlay[k.Pos] = 'K'
	}
	for _, cu := range c.Customers {
		overlay[cu.Pos] = 'H'
	}
	for _, d := range c.Depots {
		overlay[d.Pos] = 'D'
	}
	for _, ch := range c.Chargers {
		overlay[ch.Pos] = 'C'
	}
	for i, car := range cars {
		if i >= 9 {
			break
		}
		overlay[car.Pos] = byte('1' + i)
	}

	rows := make([]string, 0, c.GridSize)
	for y := range c.GridSize {
		row := make([]byte, 0, c.GridSize)
		for x := range c.GridSize {
			p := Position{x, y}
			// Unexplored tiles hide everything on them, including entity
			// glyphs. Checking this BEFORE the overlay matters: otherwise a
			// kitchen or charger in a hidden district leaks its position and
			// the fog is decorative rather than a real gate.
			if c.Hidden[y][x] {
				row = append(row, '?')
				continue
			}
			if g, ok := overlay[p]; ok {
				row = append(row, g)
				continue
			}
			if g, ok := glyph[c.Grid[y][x]]; ok {
				row = append(row, g)
				continue
			}
			row = append(row, ' ')
		}
		rows = append(rows, string(row))
	}
	return rows
}

// RenderCar is the minimal car information the ASCII renderer needs.
type RenderCar struct {
	Pos Position
}

// Summary returns a short human-readable description of the city, used by the
// CLI to confirm a load succeeded.
func (c *City) Summary() string {
	var items []string
	for k := range c.Items {
		items = append(items, k)
	}
	sort.Strings(items)
	return fmt.Sprintf("%s %dx%d: %d kitchens, %d customers, %d depots, %d chargers, items [%s]",
		c.Name, c.GridSize, c.GridSize,
		len(c.Kitchens), len(c.Customers), len(c.Depots), len(c.Chargers),
		strings.Join(items, " "))
}

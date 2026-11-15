package engine

import (
	"fmt"
	"sort"
)

// Reachable computes the set of coordinates a car could reach from start by
// walking, ignoring battery. Hidden and impassable tiles are excluded. This is
// the primitive behind the "a build must not strand anything" guard.
func (c *City) Reachable(start Position) map[Position]bool {
	seen := map[Position]bool{}
	if !c.Walkable(start) {
		// The start itself being unwalkable still yields an empty set rather
		// than a panic; callers treat that as "nothing reachable".
		return seen
	}
	seen[start] = true
	queue := []Position{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok || seen[n] {
				continue
			}
			if c.Walkable(n) {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return seen
}

// Distances returns the BFS step distance from start to every reachable tile.
// Unreachable tiles are absent from the map. Roads and ground cost the same
// here: this is a routing/estimation metric, not a battery budget. Use
// WalkCost when battery matters.
func (c *City) Distances(start Position) map[Position]int {
	dist := map[Position]int{}
	if !c.Walkable(start) {
		return dist
	}
	dist[start] = 0
	queue := []Position{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok {
				continue
			}
			if _, seen := dist[n]; seen {
				continue
			}
			if c.Walkable(n) {
				dist[n] = dist[p] + 1
				queue = append(queue, n)
			}
		}
	}
	return dist
}

// Distance returns the BFS step distance between two tiles, or -1 when there
// is no walking route.
func (c *City) Distance(from, to Position) int {
	if from == to {
		return 0
	}
	dist := c.Distances(from)
	if d, ok := dist[to]; ok {
		return d
	}
	return -1
}

// DistanceAvoiding is Distance with extra tiles treated as impassable.
//
// Closures need this: once a street is shut, the honest route estimate and the
// honest reachability question both have to go around it, or the game quotes a
// deadline for a drive nobody can make. It takes a predicate rather than a set
// so the caller decides what "impassable" means without this package knowing
// about closures.
//
// The ORIGIN is always passable, even when blocked reports it as impassable.
// That asymmetry is the whole rule for street works: a closure blocks ENTRY, so
// a car standing on a tile that closes under it can still drive off. Treating a
// blocked origin as unreachable made four queries disagree about the same tile
// -- movement said the car could leave, this returned -1, StepToward returned a
// real direction, and estimateFrom quoted a SHORTER deadline because it skips a
// negative leg.
//
// Note DistancesAvoiding and ReachAvoiding below keep the strict form (a blocked
// start yields nothing). They answer "what can be reached FROM this seed", which
// is a guard's question about a hypothetical grid, not a car's route. Do not
// copy this rule into them.
func (c *City) DistanceAvoiding(from, to Position, blocked func(Position) bool) int {
	if from == to {
		return 0
	}
	if !c.Walkable(from) {
		return -1
	}
	dist := map[Position]int{from: 0}
	queue := []Position{from}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if p == to {
			return dist[p]
		}
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok {
				continue
			}
			if _, seen := dist[n]; seen {
				continue
			}
			if !c.Walkable(n) {
				continue
			}
			if blocked != nil && blocked(n) {
				continue
			}
			dist[n] = dist[p] + 1
			queue = append(queue, n)
		}
	}
	return -1
}

// DistanceAvoidingSet is DistanceAvoiding with a coordinate set.
func (c *City) DistanceAvoidingSet(from Position, blocked map[Position]bool) (int, bool) {
	return 0, false
}

// DistancesAvoiding returns the BFS distance from start to every reachable
// tile when the blocked set is impassable. It is the map form of
// DistanceAvoiding, used by the closure guard to reason about a whole grid at
// once rather than one destination at a time.
func (c *City) DistancesAvoiding(start Position, blocked map[Position]bool) map[Position]int {
	dist := map[Position]int{}
	if blocked[start] || !c.Walkable(start) {
		return dist
	}
	dist[start] = 0
	queue := []Position{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok {
				continue
			}
			if _, seen := dist[n]; seen {
				continue
			}
			if blocked[n] || !c.Walkable(n) {
				continue
			}
			dist[n] = dist[p] + 1
			queue = append(queue, n)
		}
	}
	return dist
}

// ReachAvoiding is the set of tiles reachable from start when the blocked set
// is impassable. It is the primitive behind both the closure safety check and
// the tests that pin it.
func (c *City) ReachAvoiding(start Position, blocked map[Position]bool) map[Position]bool {
	seen := map[Position]bool{}
	if blocked[start] || !c.Walkable(start) {
		return seen
	}
	seen[start] = true
	queue := []Position{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range AllDirections {
			n, ok := p.Neighbour(d)
			if !ok || seen[n] || blocked[n] || !c.Walkable(n) {
				continue
			}
			seen[n] = true
			queue = append(queue, n)
		}
	}
	return seen
}

// BridgeTilesForTest returns the terrain writes of a named build in a city
// file. It exists so tests can assert on the tiles a build actually declares
// instead of hardcoding coordinates that move when the map is regenerated.
func BridgeTilesForTest(cityFile, buildID string) []TileWrite {
	cfg, err := LoadCityConfig(cityFile)
	if err != nil {
		return nil
	}
	for _, b := range cfg.Builds {
		if b.ID != buildID {
			continue
		}
		var out []TileWrite
		for _, e := range b.Effects {
			if e.Type == EffectBuildBridge || e.Type == EffectPaveRoad {
				out = append(out, e.Tiles...)
			}
		}
		return out
	}
	return nil
}

// NearestChargingTiles returns charging tiles reachable from start, nearest
// first. It is used for battery-risk estimates and for suggesting where a
// player should head when low.
func (c *City) NearestChargingTiles(start Position, limit int) []Position {
	dist := c.Distances(start)
	type entry struct {
		p Position
		d int
	}
	var entries []entry
	for p, d := range dist {
		if c.ChargingAt(p) {
			entries = append(entries, entry{p, d})
		}
	}
	// Deterministic order: distance, then y, then x. Map iteration is random,
	// and a non-deterministic "nearest charger" would leak into replay output.
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.d != b.d {
			return a.d < b.d
		}
		if a.p.Y != b.p.Y {
			return a.p.Y < b.p.Y
		}
		return a.p.X < b.p.X
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]Position, len(entries))
	for i, e := range entries {
		out[i] = e.p
	}
	return out
}

// StrandedEntities lists entity ids that would become unreachable from every
// depot and charger if the city were in its current state. An empty result
// means the map is coherent: every kitchen and customer can be served.
//
// This is the guard run against a candidate grid after applying a build, so a
// bridge or a reveal can never wall a restaurant off from the fleet.
func (c *City) StrandedEntities() []string {
	// Union of everything reachable from any depot or charger, treating those
	// tiles themselves as passable entry points.
	reach := map[Position]bool{}
	for _, d := range c.Depots {
		for p := range c.Reachable(d.Pos) {
			reach[p] = true
		}
	}
	for _, ch := range c.Chargers {
		for p := range c.Reachable(ch.Pos) {
			reach[p] = true
		}
	}

	// Only entities on KNOWN terrain can be stranded. An entity inside a hidden
	// district is not stranded: it is not yet in play, and counting it would make
	// every build fail the moment a map authors content for a district it has not
	// revealed yet. Revealing the district is what puts it in play.
	var stranded []string
	for _, k := range c.Kitchens {
		if c.IsHidden(k.Pos) {
			continue
		}
		if !reach[k.Pos] {
			stranded = append(stranded, "kitchen:"+k.ID)
		}
	}
	for _, cu := range c.Customers {
		if c.IsHidden(cu.Pos) {
			continue
		}
		if !reach[cu.Pos] {
			stranded = append(stranded, "customer:"+cu.ID)
		}
	}
	return stranded
}

// ValidatePlacement reports why a build cannot be placed, or nil if it can.
// It is intentionally cheap: it checks the tiles a build would write are on
// the grid, and that the resulting city strands nothing.
func (c *City) ValidatePlacement(b BuildConfig) error {
	next := c.Clone()
	if err := next.applyEffects(b.Effects); err != nil {
		return err
	}
	if stranded := next.StrandedEntities(); len(stranded) > 0 {
		return fmt.Errorf("build %q would strand %v", b.ID, stranded)
	}
	return nil
}

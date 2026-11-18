package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
)

func loadTerrainMap(t *testing.T, id string) *engine.Map {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "maps", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg engine.MapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	m, err := engine.NewMap(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestTerrainMapsNeedTheirTiles guards the four teaching maps: each ships a
// tile the tape size cannot work around, so the mechanic cannot be ignored.
func TestTerrainMapsNeedTheirTiles(t *testing.T) {
	cases := []struct {
		id   string
		has  func(*engine.Map) bool
		why  string
		need int // instructions a tile-free route needs at minimum
	}{
		{"belt_run", func(m *engine.Map) bool { return len(m.Conveyors) > 0 },
			"belts halve the top run; tile-free the route is 16 actions", 16},
		{"mirror_hall", func(m *engine.Map) bool { return len(m.Mirrors) > 0 },
			"turning by hand adds four TURN slots", 23},
		{"key_garden", func(m *engine.Map) bool { return len(m.Keys) > 0 && len(m.Doors) > 0 },
			"the door blocks until the key is taken", 0},
		{"portal_hop", func(m *engine.Map) bool { return len(m.Portals) == 2 },
			"the wall leaves only the portal, 9 cells away", 9},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			m := loadTerrainMap(t, c.id)
			if res := Check(m.Config()); !res.Valid {
				t.Fatalf("map is not valid: %v", res.Issues)
			}
			if !c.has(m) {
				t.Fatal("map does not use the tile it is meant to teach")
			}
			if c.need > 0 && m.TotalSlots() >= c.need {
				t.Fatalf("tape has %d slots, tile-free route needs %d: the map does not force the tile", m.TotalSlots(), c.need)
			}
		})
	}
}

// TestBeltRunStraightRunStalls guards Terrain 1's trick: the upper belt hands
// the robot to belts pointing back, so a program of nothing but MOVEs oscillates
// between two cells and never reaches the treat.
func TestBeltRunStraightRunStalls(t *testing.T) {
	m := loadTerrainMap(t, "belt_run")
	main := make([]engine.Instruction, m.MainTapeLength)
	for i := range main {
		main[i] = engine.MoveForward
	}
	p := engine.Program{Main: main}
	st := engine.Run(m, p)
	if st.Status == engine.StatusWon {
		t.Fatalf("a straight run of MOVEs won in %d steps: the belt never sends the robot back", st.Steps)
	}
	if st.TreatsLeft != len(m.Treats) {
		t.Fatalf("straight run collected %d treats, want 0: it should not get near them", len(m.Treats)-st.TreatsLeft)
	}
	if end := st.Visited[len(st.Visited)-1]; end.Y != 0 {
		t.Fatalf("straight run ended at %v, want it stuck on the belt row", end)
	}
}

// TestMirrorHallForcesMirrors guards Terrain 2: the route has four corners and
// the tape has exactly the MOVEs, so every TURN a tile-free program pays pushes
// it over the slot count.
func TestMirrorHallForcesMirrors(t *testing.T) {
	m := loadTerrainMap(t, "mirror_hall")
	if res := Check(m.Config()); !res.Valid {
		t.Fatalf("map is not valid: %v", res.Issues)
	}
	if len(m.Mirrors) != 4 {
		t.Fatalf("mirrors = %d, want 4 (one per corner)", len(m.Mirrors))
	}
	const tileFree = 23 // 19 MOVEs along the snake + one TURN per corner
	if got := minActions(m); got != tileFree {
		t.Fatalf("tile-free minimum actions = %d, want %d", got, tileFree)
	}
	if slots := m.TotalSlots(); slots >= tileFree {
		t.Fatalf("tape has %d slots, the tile-free route needs %d: the maps does not force the mirrors", slots, tileFree)
	}
}

// TestKeyGardenDoorBlocksWithoutKey proves the gate is real, not decorative.
func TestKeyGardenDoorBlocksWithoutKey(t *testing.T) {
	m := loadTerrainMap(t, "key_garden")
	st := engine.NewVMState(m)
	p := engine.Program{Main: []engine.Instruction{engine.MoveForward, engine.MoveForward}}
	st.Settle(&p)
	ev, err := st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Blocked || st.Pos != (engine.Position{X: 1, Y: 0}) {
		t.Fatalf("first move blocked=%v pos=%v, want a free cell", ev.Blocked, st.Pos)
	}
	ev, err = st.Step(m, &p)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Blocked || st.Pos != (engine.Position{X: 1, Y: 0}) {
		t.Fatalf("blocked=%v pos=%v, want the closed door to stop the robot", ev.Blocked, st.Pos)
	}
}

// TestKeyGardenDoorIsTheOnlyWayIn guards the puzzle: with the door shut (no key
// taken) the treat must be unreachable, or the level can be won by walking
// around the garden and the key teaches nothing.
func TestKeyGardenDoorIsTheOnlyWayIn(t *testing.T) {
	m := loadTerrainMap(t, "key_garden")
	if got := minActionsWithTiles(m, false); got != -1 {
		t.Fatalf("treat reachable in %d actions without taking the key: the door can be walked around", got)
	}
	const needed = 9 // right, turn, down for the key, up, right through the door, on to the treat
	if got := minActionsWithTiles(m, true); got != needed {
		t.Fatalf("minimum actions with the key = %d, want %d", got, needed)
	}
	if slots := m.TotalSlots(); slots < needed {
		t.Fatalf("tape has %d slots, the route needs %d", slots, needed)
	}
	// The shortcut the old layout allowed: down the left column, across the bottom
	// row and up the right column, never touching key or door.
	p := engine.Program{Main: []engine.Instruction{
		engine.TurnRight, engine.MoveForward, engine.MoveForward,
		engine.TurnLeft, engine.MoveForward, engine.MoveForward, engine.MoveForward,
		engine.TurnLeft, engine.MoveForward, engine.MoveForward,
	}}
	if st := engine.Run(m, p); st.Status == engine.StatusWon {
		t.Fatalf("the walk-around won in %d steps: the door is bypassable", st.Steps)
	}
}

func tileMirror(f engine.Facing, slash bool) engine.Facing {
	if slash {
		switch f {
		case engine.FacingUp:
			return engine.FacingRight
		case engine.FacingRight:
			return engine.FacingUp
		case engine.FacingDown:
			return engine.FacingLeft
		default:
			return engine.FacingDown
		}
	}
	switch f {
	case engine.FacingUp:
		return engine.FacingLeft
	case engine.FacingLeft:
		return engine.FacingUp
	case engine.FacingRight:
		return engine.FacingDown
	default:
		return engine.FacingRight
	}
}

// minActionsWithTiles is minActions with the map's tiles applied: the fewest
// TURN/MOVE actions that collect every treat, BFS over (position, facing, open
// doors, collected treats). It ignores tape lengths and calls, so it is a lower
// bound for any program. With keys false a stepped-on key never opens its door,
// which asks whether a door can be walked around instead of unlocked.
func minActionsWithTiles(m *engine.Map, keys bool) int {
	type state struct {
		pos    engine.Position
		facing engine.Facing
		doors  int
		treats int
	}
	dirs := [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}
	treatBit := map[engine.Position]int{}
	for i, t := range m.Treats {
		treatBit[t] = 1 << i
	}
	full := 1<<len(m.Treats) - 1
	closed := func(mask, x, y int) bool {
		l := m.DoorAt(x, y)
		if l == 0 {
			return false
		}
		i := m.DoorIndex(l)
		return i < 0 || mask&(1<<i) == 0
	}
	// apply arranges the robot after a successful move onto from+dir.
	apply := func(s state) (engine.Position, engine.Facing, int) {
		d := dirs[s.facing]
		p := engine.Position{X: s.pos.X + d[0], Y: s.pos.Y + d[1]}
		facing, doors := s.facing, s.doors
		if twin, ok := m.PortalTwin(p.X, p.Y); ok {
			p = twin
		} else if slash, ok := m.MirrorAt(p.X, p.Y); ok {
			facing = tileMirror(facing, slash)
		} else if dir, ok := m.ConveyorAt(p.X, p.Y); ok {
			dd := dirs[dir]
			next := engine.Position{X: p.X + dd[0], Y: p.Y + dd[1]}
			if !m.IsRock(next.X, next.Y) && !closed(doors, next.X, next.Y) {
				p = next
			}
		}
		if l := m.KeyAt(p.X, p.Y); keys && l != 0 {
			if i := m.DoorIndex(l); i >= 0 {
				doors |= 1 << i
			}
		}
		return p, facing, doors
	}
	start := state{pos: m.Start, facing: m.StartFacing}
	start.treats |= treatBit[m.Start]
	dist := map[state]int{start: 0}
	queue := []state{start}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s.treats == full {
			return dist[s]
		}
		next := []state{
			{pos: s.pos, facing: (s.facing + 3) % 4, doors: s.doors, treats: s.treats},
			{pos: s.pos, facing: (s.facing + 1) % 4, doors: s.doors, treats: s.treats},
		}
		d := dirs[s.facing]
		tx, ty := s.pos.X+d[0], s.pos.Y+d[1]
		if !m.IsRock(tx, ty) && !closed(s.doors, tx, ty) {
			p, facing, doors := apply(s)
			next = append(next, state{pos: p, facing: facing, doors: doors, treats: s.treats | treatBit[p]})
		}
		for _, n := range next {
			if _, seen := dist[n]; !seen {
				dist[n] = dist[s] + 1
				queue = append(queue, n)
			}
		}
	}
	return -1
}

// TestTerrainHardMapsForceRecursion proves the two hard terrain maps cannot be
// solved with a flat or merely nested program: the actions the route needs
// exceed what main + one sub can execute without recursion.
func TestTerrainHardMapsForceRecursion(t *testing.T) {
	cases := []struct {
		id      string
		actions int
	}{
		{"mirror_circuit", 17}, // one lap: mirrors turn, so no TURN is needed
		{"belt_ladder", 22},    // four belt runs plus the gaps between them
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			m := loadTerrainMap(t, c.id)
			if !m.AllowRecursion {
				t.Fatal("map must allow recursion")
			}
			if got := minActionsWithTiles(m, true); got != c.actions {
				t.Fatalf("minimum actions with tiles = %d, want %d", got, c.actions)
			}
			if ceiling := maxLeafActions(m, 1); ceiling >= c.actions {
				t.Fatalf("one nesting level runs %d actions, route needs %d: recursion not forced", ceiling, c.actions)
			}
			sol, err := LoadSolution("../solutions", c.id)
			if err != nil {
				t.Skipf("no reference solution: %v", err)
			}
			if err := CheckSolution(m, sol); err != nil {
				t.Fatal(err)
			}
		})
	}
}

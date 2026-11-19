// Package validate checks maps (structure, treat reachability) and their
// reference solutions.
package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wricardo/drbrain/games/motor/game/engine"
)

// Result is the structured outcome of Check.
type Result struct {
	Valid             bool              `json:"valid"`
	Issues            []string          `json:"issues"`
	UnreachableTreats []engine.Position `json:"unreachableTreats"`
}

// Check validates cfg structurally (engine.NewMap) and verifies every treat
// is reachable from the start under the map's tiles and doors (a necessary
// condition: turning is assumed free, as before).
func Check(cfg engine.MapConfig) Result {
	res := Result{Issues: []string{}, UnreachableTreats: []engine.Position{}}
	m, err := engine.NewMap(cfg)
	if err != nil {
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			res.Issues = append(res.Issues, ve.Issues...)
		} else {
			res.Issues = append(res.Issues, err.Error())
		}
		return res
	}
	res.UnreachableTreats = UnreachableTreats(m)
	for _, p := range res.UnreachableTreats {
		res.Issues = append(res.Issues, fmt.Sprintf("treat at (%d,%d) is unreachable from the start", p.X, p.Y))
	}
	res.Valid = len(res.Issues) == 0
	return res
}

// UnreachableTreats returns the treats not reachable from the start. Turning
// in place is free and never limited by the map, so only the cell and the set
// of opened doors matter; arrival effects are applied exactly as Step does
// them (portal, then mirror, then conveyor push, no chaining).
func UnreachableTreats(m *engine.Map) []engine.Position {
	doors := 1 << len(m.DoorLetters)
	seen := make([]bool, m.Width*m.Height*doors)
	startMask := 0
	queue := []state{{m.Start, startMask}}
	seen[(m.Start.Y*m.Width+m.Start.X)*doors+startMask] = true
	treatAt := make(map[engine.Position]bool, len(m.Treats))
	for _, t := range m.Treats {
		treatAt[t] = true
	}
	reached := make(map[engine.Position]bool, len(m.Treats))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if treatAt[cur.pos] {
			reached[cur.pos] = true
		}
		for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
			pos, mask, ok := travel(m, cur.pos, cur.mask, d)
			if !ok {
				continue
			}
			i := (pos.Y*m.Width+pos.X)*doors + mask
			if seen[i] {
				continue
			}
			seen[i] = true
			queue = append(queue, state{pos, mask})
		}
	}
	out := []engine.Position{}
	for _, t := range m.Treats {
		if !reached[t] {
			out = append(out, t)
		}
	}
	return out
}

type state struct {
	pos  engine.Position
	mask int
}

// travel applies one attempted move in direction d from pos with the doors in
// mask opened. ok is false when the move is blocked.
func travel(m *engine.Map, pos engine.Position, mask int, d [2]int) (engine.Position, int, bool) {
	nx, ny := pos.X+d[0], pos.Y+d[1]
	if m.IsRock(nx, ny) || doorClosed(m, mask, nx, ny) {
		return pos, mask, false
	}
	at := engine.Position{X: nx, Y: ny}
	if twin, ok := m.PortalTwin(nx, ny); ok {
		return twin, mask, true
	}
	if dir, ok := m.ConveyorAt(nx, ny); ok {
		dd := [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}[dir]
		tx, ty := nx+dd[0], ny+dd[1]
		if !m.IsRock(tx, ty) && !doorClosed(m, mask, tx, ty) {
			at = engine.Position{X: tx, Y: ty}
		}
	}
	if l := m.KeyAt(at.X, at.Y); l != 0 {
		if i := m.DoorIndex(l); i >= 0 {
			mask |= 1 << i
		}
	}
	return at, mask, true
}

// doorClosed reports whether (x,y) is a door that is still closed with mask.
func doorClosed(m *engine.Map, mask, x, y int) bool {
	l := m.DoorAt(x, y)
	if l == 0 {
		return false
	}
	i := m.DoorIndex(l)
	return i < 0 || mask&(1<<i) == 0
}

// CheckSolution verifies p is a valid program for m and simulates to WON
// within the map's limits.
func CheckSolution(m *engine.Map, p engine.Program) error {
	if err := engine.ValidateProgram(m, p); err != nil {
		return fmt.Errorf("invalid program: %w", err)
	}
	st := engine.Run(m, p)
	if st.Status != engine.StatusWon {
		return fmt.Errorf("solution does not win: status=%s loss=%s steps=%d treats left=%d",
			st.Status, st.LossReason, st.Steps, st.TreatsLeft)
	}
	return nil
}

// CheckRecursionRequired verifies that for a recursion-enabled map the
// solution does NOT win when recursion is forbidden.
func CheckRecursionRequired(m *engine.Map, p engine.Program) error {
	if !m.AllowRecursion {
		return nil
	}
	cfg := m.Config()
	cfg.AllowRecursion = false
	nr, err := engine.NewMap(cfg)
	if err != nil {
		return err
	}
	if st := engine.Run(nr, p); st.Status == engine.StatusWon {
		return errors.New("map allows recursion but its solution also wins with allow_recursion=false")
	}
	return nil
}

// LoadSolution reads <dir>/<mapID>.json as a Program.
func LoadSolution(dir, mapID string) (engine.Program, error) {
	var p engine.Program
	if !engine.ValidID(mapID) {
		return p, fmt.Errorf("invalid map id %q", mapID)
	}
	path := filepath.Join(dir, mapID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return p, fmt.Errorf("solution for map %q: %w", mapID, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return engine.Program{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return engine.Program{}, fmt.Errorf("%s: unexpected data after JSON value", path)
	}
	return p, nil
}

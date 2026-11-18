// Command mapgen generates a small, provably solvable map together with its
// reference solution.
//
//	mapgen -size 5 -seed 7 -out maps-gen/maps/5x5_s7.json -solution maps-gen/solutions/5x5_s7.json
//
// A generated layout is a walled border with a rock-strewn playfield inside,
// one start cell and a few treats. The generator then searches (BFS over
// position, facing and collected-treats) for the shortest literal program that
// collects every treat, and factors a repeated block of that program into a
// sub. Only layouts whose shortest run contains such a block are emitted, and
// main_tape_length is set below the run length, so no program without subs can
// fit: the sub is required, not decorative. Subs hold literal instructions
// only (no nested calls) and max_steps covers the calls plus the run.
//
// Output is deliberately not written into maps/ by default: the shipped-map
// tests (validate/validate_test.go) require a golden step count for every file
// there. Point -out and -solution at a scratch directory, or use the justfile
// recipe `just mapgen`, then run
// `go run ./cmd/validate -maps-dir maps-gen/maps -solutions-dir maps-gen/solutions`.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/validate"
)

const (
	// rockChance is the per-cell chance an interior cell starts as rock.
	rockChance = 0.2
	// maxGeneratedTreats caps treats so the BFS state space (4 facings x 2^treats
	// masks per cell) stays small even on a full 32x32 board.
	maxGeneratedTreats = 6
	// maxAttempts is how many random layouts one treat count gets before the
	// generator tries fewer treats.
	maxAttempts = 500
	// stepSlack is headroom over the shortest run, so the map is not a
	// step-limit puzzle.
	stepSlack = 25
)

// dirs mirrors engine's facing order: 0=up, 1=right, 2=down, 3=left.
var dirs = [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// startGlyph is the layout glyph for a start cell facing 0..3.
var startGlyph = [4]byte{'^', '>', 'v', '<'}

func main() {
	size := flag.Int("size", 5, "layout side in cells, walls included (5 gives a 3x3 playfield)")
	seed := flag.Int64("seed", 1, "RNG seed; the same size, seed and treats give the same map")
	treats := flag.Int("treats", 0, "treats per map (0 = scaled to the board, 3..6, falling back to fewer)")
	out := flag.String("out", "", "write the map JSON here (empty = stdout)")
	solution := flag.String("solution", "", "write the reference program here (empty = do not write)")
	id := flag.String("id", "", "map id (default: the -out file name, else gen_<size>x<size>_s<seed>)")
	name := flag.String("name", "", "map name (default: Generated <size>x<size> seed <seed>)")
	flag.Parse()

	if *id == "" {
		if *out != "" {
			*id = strings.TrimSuffix(filepath.Base(*out), ".json")
		} else {
			*id = fmt.Sprintf("gen_%dx%d_s%d", *size, *size, *seed)
		}
	}
	if *name == "" {
		*name = fmt.Sprintf("Generated %dx%d seed %d", *size, *size, *seed)
	}

	cfg, prog, err := Generate(*size, *seed, *treats, *id, *name)
	if err != nil {
		log.Fatal(err)
	}
	mapJSON, err := encodeJSON(cfg)
	if err != nil {
		log.Fatal(err)
	}
	progJSON, err := encodeJSON(prog)
	if err != nil {
		log.Fatal(err)
	}

	if *out == "" {
		fmt.Println(string(mapJSON))
	} else if err := writeFile(*out, mapJSON); err != nil {
		log.Fatal(err)
	}
	if *solution != "" {
		if err := writeFile(*solution, progJSON); err != nil {
			log.Fatal(err)
		}
	}

	run := "unknown"
	if n, ok := runSteps(cfg); ok {
		run = fmt.Sprintf("%d steps", n)
	}
	fmt.Fprintf(os.Stderr, "mapgen: %s: %dx%d, %d treats, shortest run %s, main %d slots, subs %v, max_steps %d\n",
		cfg.ID, *size, *size, countTreats(cfg.Layout), run, cfg.MainTapeLength, cfg.SubTapeLengths, cfg.MaxSteps)
	if *solution != "" {
		fmt.Fprintf(os.Stderr, "mapgen: %s: %d-slot reference program\n", *solution, len(prog.Main))
	}
}

// Generate returns a solvable map and its reference solution. id must satisfy
// engine.ValidID; the layout side includes the rock border.
func Generate(size int, seed int64, treatCount int, id, name string) (engine.MapConfig, engine.Program, error) {
	if size < engine.MinSide || size > engine.MaxSide {
		return engine.MapConfig{}, engine.Program{}, fmt.Errorf("size %d must be %d..%d", size, engine.MinSide, engine.MaxSide)
	}
	if size < 4 {
		// A 3x3 board is a single playfield cell: the start has no free
		// neighbour to move into, so no treat can be collected.
		return engine.MapConfig{}, engine.Program{}, fmt.Errorf("size %d is too small: the playfield needs at least 2x2 cells", size)
	}
	if !engine.ValidID(id) {
		return engine.MapConfig{}, engine.Program{}, fmt.Errorf("id %q must match %s", id, "^[a-z0-9_-]{1,64}$")
	}

	// Try the requested treat count, or the scaled count down to 1 when it is
	// left to us: a sparse board may not have a solvable layout that also
	// fits the main tape.
	wants := []int{treatCount}
	if treatCount <= 0 {
		for n := autoTreats(size); n >= 1; n-- {
			wants = append(wants, n)
		}
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	for _, want := range wants {
		for range maxAttempts {
			cfg, prog, ok := tryLayout(rng, size, want, seed, id, name)
			if ok {
				return cfg, prog, nil
			}
		}
	}
	return engine.MapConfig{}, engine.Program{}, fmt.Errorf(
		"no %dx%d layout had a repeated block whose calls fit the %d-slot main tape; try -treats 1 or a smaller -size",
		size, size, engine.MaxMainTapeLength)
}

// autoTreats scales the treat count with the playfield: three treats on a 20x20
// board leaves most of it empty.
func autoTreats(size int) int {
	area := (size - 2) * (size - 2)
	return min(maxGeneratedTreats, max(3, area/70))
}

// tryLayout builds one random candidate and accepts it only when its shortest
// literal program fits the main tape and actually wins.
func tryLayout(rng *rand.Rand, size, treatCount int, seed int64, id, name string) (engine.MapConfig, engine.Program, bool) {
	rock := make([]bool, size*size)
	for y := range size {
		for x := range size {
			rock[y*size+x] = x == 0 || y == 0 || x == size-1 || y == size-1
		}
	}
	var free []engine.Position
	for y := 1; y < size-1; y++ {
		for x := 1; x < size-1; x++ {
			if rng.Float64() < rockChance {
				rock[y*size+x] = true
				continue
			}
			free = append(free, engine.Position{X: x, Y: y})
		}
	}
	if treatCount < 1 || len(free)-1 < treatCount {
		return engine.MapConfig{}, engine.Program{}, false
	}

	start := free[rng.IntN(len(free))]
	var facings []engine.Facing
	for f, d := range dirs {
		if !rock[(start.Y+d[1])*size+(start.X+d[0])] {
			facings = append(facings, engine.Facing(f))
		}
	}
	if len(facings) == 0 {
		return engine.MapConfig{}, engine.Program{}, false
	}
	facing := facings[rng.IntN(len(facings))]

	// Treats: a shuffled sample of the free cells that are not the start.
	rest := make([]engine.Position, 0, len(free)-1)
	for _, p := range free {
		if p != start {
			rest = append(rest, p)
		}
	}
	rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	treats := rest[:treatCount]

	grid := make([][]byte, size)
	for y := range grid {
		grid[y] = make([]byte, size)
		for x := range grid[y] {
			grid[y][x] = '.'
			if rock[y*size+x] {
				grid[y][x] = '#'
			}
		}
	}
	for _, t := range treats {
		grid[t.Y][t.X] = '*'
	}
	grid[start.Y][start.X] = startGlyph[facing]
	layout := make([]string, size)
	for y := range grid {
		layout[y] = string(grid[y])
	}

	cfg := engine.MapConfig{
		ID: id, Name: name, Difficulty: "easy", Layout: layout,
		SubTapeLengths: []int{}, MaxCallDepth: 5,
	}
	m, err := engine.NewMap(cfg)
	if err != nil {
		return engine.MapConfig{}, engine.Program{}, false
	}
	actions, ok := shortestProgram(m)
	if !ok {
		return engine.MapConfig{}, engine.Program{}, false
	}
	main, subs, calls := decompose(actions)
	if len(subs) == 0 || len(main) > engine.MaxMainTapeLength {
		// Nothing repeats, or the calls do not fit the main tape: either way
		// the subs cannot carry the run.
		return engine.MapConfig{}, engine.Program{}, false
	}

	lengths := make([]int, len(subs))
	for i, sub := range subs {
		lengths[i] = len(sub)
	}
	cfg.MainTapeLength = len(main)
	cfg.SubTapeLengths = lengths
	cfg.MaxCallDepth = max(5, len(subs)+1)
	cfg.MaxSteps = len(actions) + calls + stepSlack
	cfg.Description = fmt.Sprintf(
		"Generated by cmd/mapgen (seed %d): %d treats on a %dx%d layout. The shortest run is %d steps; a block that repeats inside it is factored into %d sub(s), so the %d-slot main tape cannot hold the run.",
		seed, len(m.Treats), size, size, len(actions), len(subs), len(main))
	m, err = engine.NewMap(cfg)
	if err != nil {
		return engine.MapConfig{}, engine.Program{}, false
	}
	prog := engine.Program{Main: main, Subs: subs}
	if err := validate.CheckSolution(m, prog); err != nil {
		return engine.MapConfig{}, engine.Program{}, false
	}
	if res := validate.Check(cfg); !res.Valid {
		return engine.MapConfig{}, engine.Program{}, false
	}
	return cfg, prog, true
}

// decompose factors repeated blocks of the action sequence into subs so the
// main tape holds calls instead of literals. A block of length L repeated N
// times saves N*(L-1) main slots. Blocks that already hold a call are skipped,
// so every sub is literal and the program nests exactly one level.
func decompose(actions []engine.Instruction) (main []engine.Instruction, subs [][]engine.Instruction, calls int) {
	main = append([]engine.Instruction(nil), actions...)
	for len(subs) < engine.MaxSubs {
		start, length, count, _, ok := bestRepeat(main)
		if !ok {
			break
		}
		subs = append(subs, append([]engine.Instruction(nil), main[start:start+length]...))
		call := engine.Instruction(fmt.Sprintf("CALL_SUB_%d", len(subs)))
		out := make([]engine.Instruction, 0, len(main)-count*length+count)
		out = append(out, main[:start]...)
		for range count {
			out = append(out, call)
		}
		out = append(out, main[start+count*length:]...)
		main = out
		calls += count
	}
	return main, subs, calls
}

// bestRepeat finds the block that saves the most main slots when one call per
// occurrence replaces it. Blocks longer than a sub tape are never candidates.
func bestRepeat(actions []engine.Instruction) (start, length, count, saved int, ok bool) {
	for l := 2; l <= engine.MaxSubTapeLength && 2*l <= len(actions); l++ {
		for s := 0; s+2*l <= len(actions); s++ {
			block := actions[s : s+l]
			if hasCall(block) {
				continue
			}
			n := 1
			for s+(n+1)*l <= len(actions) && slices.Equal(block, actions[s+n*l:s+(n+1)*l]) {
				n++
			}
			if n < 2 {
				continue
			}
			if save := n * (l - 1); save > saved {
				start, length, count, saved, ok = s, l, n, save, true
			}
		}
	}
	return start, length, count, saved, ok
}

// hasCall reports whether a tape holds a CALL_SUB instruction.
func hasCall(tape []engine.Instruction) bool {
	for _, ins := range tape {
		if _, ok := ins.SubIndex(); ok {
			return true
		}
	}
	return false
}

// runSteps reports the shortest literal run for a generated map, for the
// summary line.
func runSteps(cfg engine.MapConfig) (int, bool) {
	m, err := engine.NewMap(cfg)
	if err != nil {
		return 0, false
	}
	actions, ok := shortestProgram(m)
	if !ok {
		return 0, false
	}
	return len(actions), true
}

// shortestProgram finds the fewest TURN/MOVE actions that collect every treat
// and returns them in order. Generated maps have no portals, conveyors, keys
// or doors, so plain movement is exact; blocked moves are skipped because they
// only waste a step.
func shortestProgram(m *engine.Map) ([]engine.Instruction, bool) {
	type state struct {
		pos    engine.Position
		facing engine.Facing
		mask   int
	}
	if len(m.Treats) == 0 {
		return nil, false
	}
	treatBit := make(map[engine.Position]int, len(m.Treats))
	for i, t := range m.Treats {
		treatBit[t] = 1 << i
	}
	full := 1<<len(m.Treats) - 1

	start := state{m.Start, m.StartFacing, 0}
	states := []state{start}
	parent := []int{-1}
	via := []engine.Instruction{engine.Empty}
	seen := map[state]int{start: 0}

	for head := 0; head < len(states); head++ {
		cur := states[head]
		if cur.mask == full {
			var rev []engine.Instruction
			for i := head; parent[i] >= 0; i = parent[i] {
				rev = append(rev, via[i])
			}
			out := make([]engine.Instruction, len(rev))
			for i := range rev {
				out[i] = rev[len(rev)-1-i]
			}
			return out, true
		}
		next := []struct {
			s   state
			ins engine.Instruction
		}{
			{state{cur.pos, (cur.facing + 3) % 4, cur.mask}, engine.TurnLeft},
			{state{cur.pos, (cur.facing + 1) % 4, cur.mask}, engine.TurnRight},
		}
		d := dirs[cur.facing]
		np := engine.Position{X: cur.pos.X + d[0], Y: cur.pos.Y + d[1]}
		if !m.IsRock(np.X, np.Y) {
			next = append(next, struct {
				s   state
				ins engine.Instruction
			}{state{np, cur.facing, cur.mask | treatBit[np]}, engine.MoveForward})
		}
		for _, n := range next {
			if _, ok := seen[n.s]; ok {
				continue
			}
			seen[n.s] = len(states)
			states = append(states, n.s)
			parent = append(parent, head)
			via = append(via, n.ins)
		}
	}
	return nil, false
}

// countTreats reports how many treats a layout holds.
func countTreats(layout []string) int {
	n := 0
	for _, row := range layout {
		n += strings.Count(row, "*")
	}
	return n
}

// encodeJSON indents without escaping HTML, so a start glyph stays ">" in the
// layout instead of "\u003e".
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// writeFile writes data plus a trailing newline, creating parent directories.
func writeFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

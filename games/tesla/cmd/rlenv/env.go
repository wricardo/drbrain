package main

import (
	"fmt"
	"strings"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
)

// GridPad is the fixed observation size. Smaller maps are padded with walls;
// maps larger than GridPad are rejected (a neural net needs a fixed input).
const GridPad = 20

// Observation planes (each GridPad x GridPad, one byte per cell).
const (
	planeWall        = iota // building, water, or padding outside the map
	planeUnknown            // fog of war: the car has never seen this cell
	planeHome               // H
	planeCharger            // S
	planePark               // park not yet visited
	planePlayer             // the car
	planeNorth              // cell lets you move north (all four = 1 on plain roads)
	planeSouth              // ... south
	planeEast               // ... east
	planeWest               // ... west
	planeSafe               // reachable now AND a charger can still be reached after arriving
	planeDistPlayer         // (byte) road distance from the car to this cell (see distByte)
	planeDistCharger        // (byte) distance from this cell to the nearest charger
	planeBatLeft            // (byte) best battery you can arrive with; 0 = unreachable
	planeVisitAge           // (byte) how recently the car drove over this cell; 255 = now, 0 = faded
	NumPlanes
	numBinaryPlanes = planeDistPlayer // planes below this index hold 0/1, the rest hold 0..255
)

// visitForget is how many moves it takes a visited cell to fade to 0. A binary
// "ever visited" grid goes flat once most of the map is covered, which is when
// the car starts looping; the fading version keeps showing where it just was.
const visitForget = 24

// Actions, in the order the network emits them.
var actions = [4]string{"up", "down", "left", "right"}
var actionDelta = [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}

// rewards are the reward-shaping constants. They are plain numbers on purpose:
// tune them here, not in the network.
type rewards struct {
	Park     float64 // collecting a park
	Victory  float64 // bonus for the last park
	Death    float64 // run ended without winning
	Step     float64 // paid on every move
	Progress float64 // per cell of road-distance closed on the nearest park
	Illegal  float64 // charged for an illegal action when the env rejects it
	Reveal   float64 // paid per newly seen cell (only useful with fog)
}

var defaultRewards = rewards{Park: 1, Victory: 5, Death: -2, Step: -0.01, Progress: 0.05, Illegal: 0.05, Reveal: 0.02}

// safetyGamma must match the trainer's discount factor for the battery-safety
// shaping term to leave the optimal policy unchanged.
const safetyGamma = 0.995

// env wraps one GameEngine and turns it into reset()/step() for a learner.
type env struct {
	eng       *engine.GameEngine
	cfg       *engine.GameConfig
	rw        rewards
	prevDist  int
	lastVisit []int   // step index of the last visit per cell; -1 = never
	tick      int     // moves since the current run started
	sincePark int     // moves since the last park was collected
	fog       int     // visibility radius in cells; 0 = the whole map is known (no fog)
	known     []bool  // cells the car has seen so far (all true when fog is off)
	safety    float64 // weight of the battery-safety shaping term; 0 disables it
	reject    bool    // treat an illegal action as a wasted step instead of letting it crash
	prevPhi   float64 // safety potential at the previous state
}

// stepResult is what one step reports back to Python.
type stepResult struct {
	Reward    float64
	Terminal  bool   // game ended for a game reason (win, battery, crash)
	Truncated bool   // ended by the move cap
	Reason    string // "", "victory", "stranded", "max_moves", "no_moves", ...
	Mask      [4]bool
	Battery   int
	MaxBat    int
	Parks     int
	Left      int
	MoveLeft  int // moves left before the per-run cap ends the game
	SincePark int // moves since the last park was collected
}

func newEnv(rw rewards) *env { return &env{rw: rw} }

// remoteState is the shape of Tesla_GameState as GraphQL returns it, cut down to
// what an observation needs. It lets a live game (played over GraphQL) be fed
// through exactly the same observation code the training environment uses.
type remoteState struct {
	Grid [][]struct {
		Type              string   `json:"type"`
		Visited           bool     `json:"visited"`
		ID                string   `json:"id"`
		AllowedDirections []string `json:"allowedDirections"`
	} `json:"grid"`
	PlayerPos  struct{ X, Y int } `json:"playerPos"`
	Battery    int                `json:"battery"`
	MaxBattery int                `json:"maxBattery"`
	Score      int                `json:"score"`
}

// loadRemote installs a live game state without touching the game rules: no map
// validation runs, because the server already owns the authoritative state.
func (e *env) loadRemote(st remoteState, visited [][2]int) error {
	h := len(st.Grid)
	if h == 0 || len(st.Grid[0]) == 0 {
		return fmt.Errorf("empty grid")
	}
	w := len(st.Grid[0])
	if h > GridPad || w > GridPad {
		return fmt.Errorf("grid %dx%d is larger than the %d observation", w, h, GridPad)
	}
	grid := make([][]engine.Cell, h)
	for y := range grid {
		grid[y] = make([]engine.Cell, w)
		for x, c := range st.Grid[y] {
			grid[y][x] = engine.Cell{
				Type: engine.CellType(c.Type), Visited: c.Visited, ID: c.ID,
				AllowedDirections: c.AllowedDirections,
			}
		}
	}
	gs := &engine.GameState{
		Grid: grid, PlayerPos: engine.Position{X: st.PlayerPos.X, Y: st.PlayerPos.Y},
		Battery: st.Battery, MaxBattery: st.MaxBattery, Score: st.Score,
		VisitedParks: map[string]bool{},
	}
	for _, row := range grid {
		for _, c := range row {
			if c.Type == engine.Park && c.Visited {
				gs.VisitedParks[c.ID] = true
			}
		}
	}
	e.eng = &engine.GameEngine{}
	if err := e.eng.SetState(gs); err != nil {
		return err
	}
	e.cfg = &engine.GameConfig{Name: "live", GridSize: h, MaxBattery: st.MaxBattery}
	e.lastVisit = make([]int, h*w)
	for i := range e.lastVisit {
		e.lastVisit[i] = -1
	}
	for _, p := range visited {
		if p[1] >= 0 && p[1] < h && p[0] >= 0 && p[0] < w {
			e.lastVisit[p[1]*w+p[0]] = 0
		}
	}
	e.tick, e.sincePark = 0, 0
	e.known = make([]bool, len(e.lastVisit))
	for i := range e.known {
		e.known[i] = e.fog <= 0
	}
	e.revealAround(e.eng.GetState().PlayerPos)
	e.prevDist = e.nearestParkDist()
	e.prevPhi = e.potential()
	return nil
}

// load installs cfg as the current map and starts a fresh run.
func (e *env) load(cfg *engine.GameConfig) error {
	if cfg.GridSize > GridPad {
		return fmt.Errorf("map %q is %dx%d, observation holds at most %d", cfg.Name, cfg.GridSize, cfg.GridSize, GridPad)
	}
	eng, err := engine.NewEngineForLoad(cfg)
	if err != nil {
		return err
	}
	e.eng, e.cfg = eng, cfg
	e.lastVisit = make([]int, cfg.GridSize*cfg.GridSize)
	for i := range e.lastVisit {
		e.lastVisit[i] = -1
	}
	e.tick, e.sincePark = 0, 0
	e.known = make([]bool, len(e.lastVisit))
	for i := range e.known {
		e.known[i] = e.fog <= 0
	}
	e.revealAround(e.eng.GetState().PlayerPos)
	e.prevDist = e.nearestParkDist()
	e.prevPhi = e.potential()
	e.lastVisit[e.eng.GetState().PlayerPos.Y*cfg.GridSize+e.eng.GetState().PlayerPos.X] = 0
	return nil
}

func (e *env) mask() (m [4]bool, any bool) {
	st := e.eng.GetState()
	w := len(st.Grid[0])
	for i := range actions {
		if e.fog > 0 {
			nx, ny := st.PlayerPos.X+actionDelta[i][0], st.PlayerPos.Y+actionDelta[i][1]
			if ny < 0 || ny >= len(st.Grid) || nx < 0 || nx >= w {
				continue // off the map: known to be fatal
			}
			if !e.known[ny*w+nx] {
				m[i] = true // unexplored: allowed, but it may be a wall (that is the risk)
				any = true
				continue
			}
		}
		m[i] = e.eng.CanMove(actions[i])
		any = any || m[i]
	}
	return m, any
}

// revealAround marks cells within the visibility radius of pos as known and
// returns how many were new.
func (e *env) revealAround(pos engine.Position) int {
	if e.fog <= 0 {
		return 0
	}
	st := e.eng.GetState()
	h, w := len(st.Grid), len(st.Grid[0])
	n := 0
	for y := pos.Y - e.fog; y <= pos.Y+e.fog; y++ {
		for x := pos.X - e.fog; x <= pos.X+e.fog; x++ {
			if y < 0 || y >= h || x < 0 || x >= w || e.known[y*w+x] {
				continue
			}
			e.known[y*w+x] = true
			n++
		}
	}
	return n
}

// sees reports whether the cell is known (always true without fog).
func (e *env) sees(x, y int) bool {
	if e.fog <= 0 {
		return true
	}
	return e.known[y*len(e.eng.GetState().Grid[0])+x]
}

// step applies action a. Illegal actions are masked by the learner, but if one
// slips through it is treated as a no-op that is still charged the step cost.
func (e *env) step(a int) stepResult {
	st := e.eng.GetState()
	before := st.Score
	r := e.rw.Step
	if a >= 0 && a < 4 {
		if e.reject && !e.eng.CanMove(actions[a]) {
			// Maskable learners never choose these; recurrent ones can. Refusing the
			// input (instead of driving into the wall and dying) keeps training
			// usable and costs the step, so it cannot inflate the win rate.
			r -= e.rw.Illegal
		} else {
			e.eng.Move(actions[a])
		}
	}
	st = e.eng.GetState()

	gotPark := st.Score > before
	if gotPark {
		r += e.rw.Park
		e.sincePark = 0
	} else {
		e.sincePark++
	}
	e.tick++
	e.lastVisit[st.PlayerPos.Y*len(st.Grid[0])+st.PlayerPos.X] = e.tick
	if seen := e.revealAround(st.PlayerPos); seen > 0 {
		r += e.rw.Reveal * float64(seen)
	}
	if e.safety != 0 {
		phi := e.potential()
		r += e.safety * (safetyGamma*phi - e.prevPhi)
		e.prevPhi = phi
	}
	d := e.nearestParkDist()
	if !gotPark && d >= 0 && e.prevDist >= 0 {
		r += e.rw.Progress * float64(e.prevDist-d)
	}
	e.prevDist = d

	res := stepResult{}
	switch {
	case st.Victory:
		r += e.rw.Victory
		res.Terminal, res.Reason = true, "victory"
	case st.GameOver:
		r += e.rw.Death
		res.Reason = classify(st.Message)
		if res.Reason == "max_moves" {
			res.Truncated = true
		} else {
			res.Terminal = true
		}
	}
	if !st.GameOver {
		if _, any := e.mask(); !any { // boxed in by one-way roads and an empty battery
			r += e.rw.Death
			res.Terminal, res.Reason = true, "no_moves"
		}
	}
	res.Reward = r
	e.fill(&res)
	return res
}

func (e *env) fill(res *stepResult) {
	st := e.eng.GetState()
	res.Mask, _ = e.mask()
	res.Battery, res.MaxBat = st.Battery, st.MaxBattery
	res.Parks = engine.CountTotalParks(st.Grid)
	res.Left = res.Parks - st.Score
	res.MoveLeft = engine.MaxMovesForGrid(st.Grid) - st.CurrentMovesCount
	res.SincePark = e.sincePark
}

func classify(msg string) string {
	switch {
	case strings.Contains(msg, "Max move"):
		return "max_moves"
	case strings.Contains(msg, "Stranded"):
		return "stranded"
	case strings.Contains(msg, "Out of battery"):
		return "out_of_battery"
	case strings.Contains(msg, "crashed"):
		return "crash"
	}
	return "game_over"
}

// canStep reports whether the car may drive from (x,y) one cell in direction
// index d, honouring walls and one-way roads (same rules as the engine).
func canStep(grid [][]engine.Cell, x, y, d int) (nx, ny int, ok bool) {
	nx, ny = x+actionDelta[d][0], y+actionDelta[d][1]
	if ny < 0 || ny >= len(grid) || nx < 0 || nx >= len(grid[0]) {
		return nx, ny, false
	}
	if t := grid[ny][nx].Type; t == engine.Building || t == engine.Water {
		return nx, ny, false
	}
	card := [4]string{"north", "south", "west", "east"}[d]
	return nx, ny, allows(grid[y][x], card) && allows(grid[ny][nx], card)
}

func allows(c engine.Cell, dir string) bool {
	if len(c.AllowedDirections) == 0 {
		return true
	}
	for _, d := range c.AllowedDirections {
		if d == dir {
			return true
		}
	}
	return false
}

// bfsFrom returns road distance (in moves) from (sx,sy) to every cell; -1 means
// unreachable. Battery is ignored on purpose: that is the learner's problem.
func bfsFrom(grid [][]engine.Cell, sx, sy int, vis func(x, y int) bool) []int {
	h, w := len(grid), len(grid[0])
	dist := make([]int, h*w)
	for i := range dist {
		dist[i] = -1
	}
	dist[sy*w+sx] = 0
	queue := []int{sy*w + sx}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		x, y := cur%w, cur/w
		for d := range 4 {
			nx, ny, ok := canStep(grid, x, y, d)
			if ok && vis(nx, ny) && dist[ny*w+nx] < 0 {
				dist[ny*w+nx] = dist[cur] + 1
				queue = append(queue, ny*w+nx)
			}
		}
	}
	return dist
}

// nearestParkDist is the road distance from the car to the closest unvisited
// park, or -1 when none is reachable. It only drives reward shaping.
func (e *env) nearestParkDist() int {
	st := e.eng.GetState()
	w := len(st.Grid[0])
	dist := bfsFrom(st.Grid, st.PlayerPos.X, st.PlayerPos.Y, e.sees)
	best := -1
	for y, row := range st.Grid {
		for x, c := range row {
			if c.Type == engine.Park && !c.Visited {
				if d := dist[y*w+x]; d >= 0 && (best < 0 || d < best) {
					best = d
				}
			}
		}
	}
	return best
}

// distByte squashes a distance into 0..255: 4 units per move, capped, with 255
// meaning "unreachable".
func distByte(d int) byte {
	if d < 0 {
		return 255
	}
	if d > 62 {
		d = 62
	}
	return byte(d * 4)
}

// observe renders the current state as NumPlanes x GridPad x GridPad bytes.
func (e *env) observe(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
	plane := func(p int) []byte { return buf[p*GridPad*GridPad : (p+1)*GridPad*GridPad] }
	st := e.eng.GetState()
	h, w := len(st.Grid), len(st.Grid[0])

	for y := range GridPad {
		for x := range GridPad {
			i := y*GridPad + x
			if y >= h || x >= w {
				plane(planeWall)[i] = 1
				plane(planeDistPlayer)[i] = 255
				plane(planeDistCharger)[i] = 255
				continue
			}
			if !e.sees(x, y) {
				plane(planeUnknown)[i] = 1
				continue
			}
			c := st.Grid[y][x]
			switch c.Type {
			case engine.Building, engine.Water:
				plane(planeWall)[i] = 1
			case engine.Home:
				plane(planeHome)[i] = 1
			case engine.Supercharger:
				plane(planeCharger)[i] = 1
			case engine.Park:
				if !c.Visited {
					plane(planePark)[i] = 1
				}
			}
			for k, name := range [4]string{"north", "south", "east", "west"} {
				if allows(c, name) {
					plane(planeNorth + k)[i] = 1
				}
			}
		}
	}
	plane(planePlayer)[st.PlayerPos.Y*GridPad+st.PlayerPos.X] = 1

	// distance from the car to each cell
	dp := bfsFrom(st.Grid, st.PlayerPos.X, st.PlayerPos.Y, e.sees)
	// distance from each cell to the nearest charger (undirected multi-source BFS)
	dc := chargerDist(st.Grid, e.sees)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if age := e.tick - e.lastVisit[y*w+x]; e.lastVisit[y*w+x] >= 0 && age < visitForget {
				plane(planeVisitAge)[y*GridPad+x] = byte(255 * (visitForget - age) / visitForget)
			}
		}
	}
	for y := range h {
		for x := range w {
			plane(planeDistPlayer)[y*GridPad+x] = distByte(dp[y*w+x])
			plane(planeDistCharger)[y*GridPad+x] = distByte(dc[y*w+x])
		}
	}

	// battery planning aids: where can I go, and can I still get to a charger afterwards?
	bat := e.bestBattery()
	for y := range h {
		for x := range w {
			b := bat[y*w+x]
			if b < 0 {
				continue
			}
			plane(planeBatLeft)[y*GridPad+x] = byte(1 + b*254/max(st.MaxBattery, 1))
			if dc[y*w+x] >= 0 && b >= dc[y*w+x] {
				plane(planeSafe)[y*GridPad+x] = 1
			}
		}
	}
}

// bestBattery returns, for every cell, the largest battery level you can arrive
// with from the car's current position (chargers refill to max on the way), or
// -1 if the cell cannot be reached before the battery runs out. It is a
// longest-path relaxation; each update strictly raises a bounded integer, so it
// terminates.
func (e *env) bestBattery() []int {
	st := e.eng.GetState()
	h, w := len(st.Grid), len(st.Grid[0])
	best := make([]int, h*w)
	for i := range best {
		best[i] = -1
	}
	start := st.PlayerPos.Y*w + st.PlayerPos.X
	best[start] = st.Battery
	queue := []int{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		x, y := cur%w, cur/w
		if best[cur] <= 0 {
			continue
		}
		for d := range 4 {
			nx, ny, ok := canStep(st.Grid, x, y, d)
			if !ok || !e.sees(nx, ny) {
				continue
			}
			nb := best[cur] - 1
			if t := st.Grid[ny][nx].Type; t == engine.Home || t == engine.Supercharger {
				nb = st.MaxBattery
			}
			if nb > best[ny*w+nx] {
				best[ny*w+nx] = nb
				queue = append(queue, ny*w+nx)
			}
		}
	}
	return best
}

// chargerDist is the distance from every cell to the nearest home or
// supercharger, ignoring one-way restrictions (a lower bound on the real trip,
// which is what you want for a "will my battery last" warning). -1 = none.
func chargerDist(grid [][]engine.Cell, vis func(x, y int) bool) []int {
	h, w := len(grid), len(grid[0])
	dist := make([]int, h*w)
	for i := range dist {
		dist[i] = -1
	}
	var q []int
	for y, row := range grid {
		for x, c := range row {
			if c.Type == engine.Home || c.Type == engine.Supercharger {
				dist[y*w+x] = 0
				q = append(q, y*w+x)
			}
		}
	}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		x, y := cur%w, cur/w
		for d := range 4 {
			nx, ny := x+actionDelta[d][0], y+actionDelta[d][1]
			if ny < 0 || ny >= h || nx < 0 || nx >= w || dist[ny*w+nx] >= 0 {
				continue
			}
			if t := grid[ny][nx].Type; t == engine.Building || t == engine.Water || !vis(nx, ny) {
				continue
			}
			dist[ny*w+nx] = dist[cur] + 1
			q = append(q, ny*w+nx)
		}
	}
	return dist
}

// potential is the battery-safety potential: 0 while the car can still reach a
// charger, negative by the size of the shortfall once it cannot. Rewards shaped
// with gamma*phi(next) - phi(now) keep the optimal policy and pay the agent for
// backing out of danger early, instead of only at the final -2 death.
func (e *env) potential() float64 {
	st := e.eng.GetState()
	w := len(st.Grid[0])
	d := chargerDist(st.Grid, e.sees)[st.PlayerPos.Y*w+st.PlayerPos.X]
	if d < 0 {
		return -float64(st.MaxBattery) // no charger exists: uniformly bad
	}
	if short := d - st.Battery; short > 0 {
		return -float64(short)
	}
	return 0
}

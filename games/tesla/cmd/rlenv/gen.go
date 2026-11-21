package main

import (
	"fmt"
	"math/rand/v2"

	"github.com/wricardo/drbrain/games/tesla/game/engine"
)

// GenParams describes the random maps used for training. Python raises the
// difficulty over time (a "curriculum") by sending bigger numbers.
type GenParams struct {
	SizeMin  int     `json:"size_min"` // grid side, 7..GridPad
	SizeMax  int     `json:"size_max"`
	ParksMin int     `json:"parks_min"` // 1..6
	ParksMax int     `json:"parks_max"`
	Slack    int     `json:"slack"` // extra battery above the minimum that still wins
	Open     float64 `json:"open"`  // chance to knock out a wall: 0 = twisty maze, 0.3 = open streets
}

var legend = map[string]string{
	"B": "building", "H": "home", "P": "park", "R": "road", "S": "supercharger", "W": "water",
}

// generate builds a random solvable map. The battery is set to the smallest
// value that still lets a perfect driver win, plus Slack, so Slack directly
// controls how much room for error the learner has.
func generate(rng *rand.Rand, p GenParams) (*engine.GameConfig, error) {
	p.SizeMin = clamp(p.SizeMin, 7, GridPad)
	p.SizeMax = clamp(p.SizeMax, p.SizeMin, GridPad)
	p.ParksMin = clamp(p.ParksMin, 1, 6)
	p.ParksMax = clamp(p.ParksMax, p.ParksMin, 6)

	for range 20 {
		n := p.SizeMin + rng.IntN(p.SizeMax-p.SizeMin+1)
		layout := carve(rng, n, p.Open)
		roads := roadCells(layout)
		nParks := p.ParksMin + rng.IntN(p.ParksMax-p.ParksMin+1)
		nCharge := 1 + rng.IntN(2)
		if len(roads) < 1+nParks+nCharge {
			continue
		}
		rng.Shuffle(len(roads), func(i, j int) { roads[i], roads[j] = roads[j], roads[i] })
		set := func(c [2]int, ch byte) { layout[c[1]][c[0]] = ch }
		set(roads[0], 'H')
		for i := range nParks {
			set(roads[1+i], 'P')
		}
		for i := range nCharge {
			set(roads[1+nParks+i], 'S')
		}
		rows := make([]string, n)
		for i, r := range layout {
			rows[i] = string(r)
		}
		cfg := &engine.GameConfig{
			Name: fmt.Sprintf("gen-%dx%d", n, n), GridSize: n, Layout: rows, Legend: legend,
		}
		minBat, ok := minWinnableBattery(cfg)
		if !ok {
			continue
		}
		b := minBat + p.Slack
		if b > engine.MaxBattery {
			b = engine.MaxBattery
		}
		cfg.MaxBattery, cfg.StartingBattery = b, b
		return cfg, nil
	}
	return nil, fmt.Errorf("could not generate a map for %+v", p)
}

// minWinnableBattery binary-searches the smallest max_battery for which the
// engine's own exhaustive winnability proof succeeds. More battery never makes a
// map harder, so the predicate is monotone.
func minWinnableBattery(cfg *engine.GameConfig) (int, bool) {
	ok := func(b int) bool {
		c := *cfg
		c.MaxBattery, c.StartingBattery = b, b
		return engine.ValidateGameConfig(&c) == nil
	}
	lo, hi := 2, 60
	if !ok(hi) {
		return 0, false
	}
	for lo < hi {
		mid := (lo + hi) / 2
		if ok(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, true
}

// carve makes an n x n maze: buildings everywhere, a depth-first spanning tree
// of 1-wide corridors on the odd lattice, then `open` of the remaining wall
// cells knocked down to create loops and plazas. Roughly one in five remaining
// buildings is water instead (cosmetic: both are fatal).
func carve(rng *rand.Rand, n int, open float64) [][]byte {
	g := make([][]byte, n)
	for y := range g {
		g[y] = make([]byte, n)
		for x := range g[y] {
			g[y][x] = 'B'
		}
	}
	cells := (n - 1) / 2 // odd coordinates 1,3,..; keep a wall border
	type pt struct{ x, y int }
	seen := map[pt]bool{}
	var walk func(c pt)
	walk = func(c pt) {
		seen[c] = true
		g[2*c.y+1][2*c.x+1] = 'R'
		ds := []pt{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
		rng.Shuffle(len(ds), func(i, j int) { ds[i], ds[j] = ds[j], ds[i] })
		for _, d := range ds {
			nx := pt{c.x + d.x, c.y + d.y}
			if nx.x < 0 || nx.y < 0 || nx.x >= cells || nx.y >= cells || seen[nx] {
				continue
			}
			g[2*c.y+1+d.y][2*c.x+1+d.x] = 'R' // knock out the wall between
			walk(nx)
		}
	}
	walk(pt{rng.IntN(cells), rng.IntN(cells)})
	for y := 1; y < n-1; y++ {
		for x := 1; x < n-1; x++ {
			if g[y][x] != 'B' {
				continue
			}
			switch r := rng.Float64(); {
			case r < open:
				g[y][x] = 'R'
			case r < open+0.2*(1-open):
				g[y][x] = 'W'
			}
		}
	}
	return g
}

func roadCells(g [][]byte) [][2]int {
	var out [][2]int
	for y, row := range g {
		for x, c := range row {
			if c == 'R' {
				out = append(out, [2]int{x, y})
			}
		}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

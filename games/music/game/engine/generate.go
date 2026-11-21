package engine

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/wricardo/drbrain/games/music/game/score"
)

// maxGenerateAttempts bounds the deterministic retry loop. Derangements are
// easy to draw, so the loop only runs long for duplicate-heavy pieces where
// most arrangements sound solved.
const maxGenerateAttempts = 64

// minPuzzleMeasures is the shortest piece Generate challenges. A piece with one
// bar cannot be deranged: every arrangement equals the target, so there is no
// puzzle. There is no upper bound; every difficulty uses all the bars.
const minPuzzleMeasures = 2

// expertFlipPart is the denominator of the Expert flip density: one bar in
// expertFlipPart (rounded up) carries a horizontal flip. One third is enough
// work to feel like retrograde without turning Expert into a flip drill.
const expertFlipPart = 3

// expertMinFlips is the number of horizontal flips Expert requires: one third
// of the bars rounded up, and always at least one.
func expertMinFlips(n int) int {
	h := (n + expertFlipPart - 1) / expertFlipPart
	if h < 1 {
		return 1
	}
	return h
}

// Generate builds a puzzle over the whole of p for every difficulty. Novice
// only scrambles the bars (no flips); Expert also carries a horizontal
// (retrograde) flip on at least expertMinFlips bars and never sets FlipV;
// Genius marks at least half the bars with both flips. The result is never
// already solved and always carries the difficulty's required flips, or
// Generate returns ErrNoPuzzle. A piece with fewer than minPuzzleMeasures bars
// has no puzzle. Same piece, difficulty and seed always yield the same puzzle.
func Generate(p *score.Piece, d Difficulty, seed int64) (*Puzzle, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil piece", ErrNoPuzzle)
	}
	if !d.valid() {
		return nil, fmt.Errorf("%w: unknown difficulty %d", ErrNoPuzzle, d)
	}
	n := len(p.Measures)
	if n < minPuzzleMeasures {
		return nil, fmt.Errorf("%w: %s needs at least %d measures, piece has %d", ErrNoPuzzle, d, minPuzzleMeasures, n)
	}
	measures := p.Measures
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)))
	for range maxGenerateAttempts {
		order := derangement(rng, n)
		flips := flipsFor(d, n, rng)
		if !hasRequiredFlips(d, flips) {
			continue
		}
		slots := make([]Placement, n)
		for i, src := range order {
			slots[i] = Placement{MeasureID: measures[src].ID, Transform: flips[i]}
		}
		puzzle := &Puzzle{
			Piece:      p,
			Slots:      slots,
			Difficulty: d,
			Seed:       seed,
			initial:    slices.Clone(slots),
		}
		if puzzle.Solved() {
			continue
		}
		return puzzle, nil
	}
	return nil, fmt.Errorf("%w: every arrangement of this %d-measure piece is already solved for %s", ErrNoPuzzle, n, d)
}

// derangement returns a permutation into the player's slots with no bar left in
// its target position. After a bounded number of shuffles it falls back to the
// guaranteed cyclic shift.
func derangement(rng *rand.Rand, n int) []int {
	for range 32 {
		perm := rng.Perm(n)
		fixed := false
		for i, src := range perm {
			if i == src {
				fixed = true
				break
			}
		}
		if !fixed {
			return perm
		}
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = (i + 1) % n
	}
	return perm
}

// flipsFor draws the per-slot transform set. Novice is all-identity; Expert
// sets ONLY the horizontal flip, on expertMinFlips distinct slots; Genius marks
// at least half the slots with both flips (guaranteeing at least one H and one
// V) and randomises the rest.
func flipsFor(d Difficulty, n int, rng *rand.Rand) []Transform {
	flips := make([]Transform, n)
	switch d {
	case Novice:
		return flips
	case Expert:
		for _, i := range rng.Perm(n)[:expertMinFlips(n)] {
			flips[i] = Transform{FlipH: true}
		}
	case Genius:
		mixed := (n + 1) / 2
		order := rng.Perm(n)
		for k, i := range order {
			if k < mixed {
				flips[i] = Transform{FlipH: true, FlipV: true}
				continue
			}
			flips[i] = Transform{FlipH: rng.IntN(2) == 0, FlipV: rng.IntN(2) == 0}
		}
	}
	return flips
}

// hasRequiredFlips checks the per-difficulty guarantee Generate promises.
func hasRequiredFlips(d Difficulty, flips []Transform) bool {
	switch d {
	case Novice:
		for _, t := range flips {
			if t.FlipH || t.FlipV {
				return false
			}
		}
		return true
	case Expert:
		h := 0
		for _, t := range flips {
			if t.FlipV {
				return false
			}
			if t.FlipH {
				h++
			}
		}
		return h >= expertMinFlips(len(flips))
	case Genius:
		mixed := 0
		for _, t := range flips {
			if t.FlipH && t.FlipV {
				mixed++
			}
		}
		return anyFlip(flips, func(t Transform) bool { return t.FlipH }) &&
			anyFlip(flips, func(t Transform) bool { return t.FlipV }) &&
			2*mixed >= len(flips)
	}
	return false
}

func anyFlip(flips []Transform, pred func(Transform) bool) bool {
	for _, t := range flips {
		if pred(t) {
			return true
		}
	}
	return false
}

package engine

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/wricardo/drbrain/games/music/game/score"
)

// ErrNoPuzzle means Generate cannot build a non-solved puzzle for the
// requested difficulty: the piece has fewer than two bars, or every
// arrangement of its bars is already solved.
var ErrNoPuzzle = errors.New("engine: no puzzle possible")

// ErrIndex means a slot index is outside the puzzle.
var ErrIndex = errors.New("engine: slot index out of range")

// Placement is one measure in the player's arrangement: which piece measure
// sits in the slot and how it is flipped.
type Placement struct {
	MeasureID string
	Transform Transform
}

// Puzzle is a scrambled, flipped arrangement of a whole piece. Slots are
// indexed in the player's left-to-right arrangement order.
type Puzzle struct {
	Piece      *score.Piece
	Slots      []Placement
	Difficulty Difficulty
	Seed       int64

	// initial is the arrangement Generate started from, restored by Reset. It
	// is unexported so no caller can edit the puzzle's start in place.
	initial []Placement
}

// event is one sounding note on the absolute tick timeline.
type event struct {
	at    int
	pitch int
	dur   int
}

// Solved reports whether the current arrangement sounds exactly like the
// whole piece. Every difficulty challenges all the bars, so a puzzle whose
// slots do not cover the piece one-for-one is never solved. Measure IDs are
// ignored, so identical bars are interchangeable.
func (p *Puzzle) Solved() bool {
	if p == nil || p.Piece == nil || len(p.Slots) == 0 || len(p.Slots) != len(p.Piece.Measures) {
		return false
	}
	target := p.Piece.Measures
	byID := make(map[string]score.Measure, len(target))
	for _, m := range target {
		byID[m.ID] = m
	}
	got := make([]event, 0, len(target)*2)
	offset := 0
	for _, slot := range p.Slots {
		m, ok := byID[slot.MeasureID]
		if !ok {
			return false
		}
		m = Apply(m, slot.Transform)
		for _, n := range m.Notes {
			got = append(got, event{at: offset + n.Start, pitch: n.Pitch, dur: n.Dur})
		}
		offset += m.Len
	}
	return slices.Equal(sortEvents(got), flatten(target))
}

// Swap exchanges two slots. It never touches the shared Piece.
func (p *Puzzle) Swap(a, b int) error {
	if p == nil || a < 0 || a >= len(p.Slots) || b < 0 || b >= len(p.Slots) {
		return fmt.Errorf("%w: swap %d/%d of %d slots", ErrIndex, a, b, len(p.Slots))
	}
	p.Slots[a], p.Slots[b] = p.Slots[b], p.Slots[a]
	return nil
}

// SetTransform replaces one slot's flips. It never touches the shared Piece.
func (p *Puzzle) SetTransform(index int, t Transform) error {
	if p == nil || index < 0 || index >= len(p.Slots) {
		return fmt.Errorf("%w: slot %d of %d", ErrIndex, index, len(p.Slots))
	}
	p.Slots[index].Transform = t
	return nil
}

// Reset restores the scramble Generate started from. A puzzle not built by
// Generate has no recorded start and is left unchanged.
func (p *Puzzle) Reset() {
	if p == nil || len(p.initial) != len(p.Slots) {
		return
	}
	copy(p.Slots, p.initial)
}

// flatten returns the target stream: every measure in performance order, its
// notes placed on the absolute tick timeline.
func flatten(measures []score.Measure) []event {
	out := make([]event, 0, len(measures)*2)
	offset := 0
	for _, m := range measures {
		for _, n := range m.Notes {
			out = append(out, event{at: offset + n.Start, pitch: n.Pitch, dur: n.Dur})
		}
		offset += m.Len
	}
	return sortEvents(out)
}

// sortEvents orders events by tick, then pitch, then duration. The extra keys
// make the order total so equal streams always compare equal.
func sortEvents(events []event) []event {
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		if events[i].pitch != events[j].pitch {
			return events[i].pitch < events[j].pitch
		}
		return events[i].dur < events[j].dur
	})
	return events
}

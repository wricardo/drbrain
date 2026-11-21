package engine

import "github.com/wricardo/drbrain/games/music/game/score"

// Transform is the per-measure distortion a slot may carry.
type Transform struct {
	FlipH bool
	FlipV bool
}

// Retrograde mirrors a measure in time: start' = Len - Start - Dur. Ties never
// cross a barline, so the result stays inside the measure.
func Retrograde(m score.Measure) score.Measure {
	if len(m.Notes) == 0 {
		return m
	}
	out := m
	out.Notes = make([]score.Note, len(m.Notes))
	for i, n := range m.Notes {
		n.Start = m.Len - n.Start - n.Dur
		out.Notes[i] = n
	}
	score.SortNotes(out.Notes)
	return out
}

// Invert mirrors pitches around axis: pitch' = 2*axis - pitch.
func Invert(m score.Measure, axis int) score.Measure {
	if len(m.Notes) == 0 {
		return m
	}
	out := m
	out.Notes = make([]score.Note, len(m.Notes))
	for i, n := range m.Notes {
		n.Pitch = 2*axis - n.Pitch
		out.Notes[i] = n
	}
	score.SortNotes(out.Notes)
	return out
}

// Apply composes both flips around the fixed score axis. Both flips are
// involutions, so Apply twice with the same transform is the identity.
func Apply(m score.Measure, t Transform) score.Measure {
	if t.FlipV {
		m = Invert(m, score.Axis)
	}
	if t.FlipH {
		m = Retrograde(m)
	}
	return m
}

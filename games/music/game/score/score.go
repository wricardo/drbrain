// Package score owns the Music Region notation model, the strict score file
// format, its validator and the mapstore-backed catalogue.
//
// A score file is the single source for all four consumers of a piece:
// engraving, synthesis, the solved check and the agent text view. Files are
// tick-based (PPQ 480), carry no measure IDs and no inversion axis; both are
// derived at load.
package score

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
)

const (
	// Version is the only supported score file version.
	Version = 1
	// Axis is the fixed inversion axis: B4, the middle line of the treble
	// staff. Ingest transposes every excerpt into [Axis-12, Axis+12].
	Axis = 71
	// MinPitch and MaxPitch bound every note: |pitch-Axis| <= 12.
	MinPitch = Axis - 12
	MaxPitch = Axis + 12
	// ClefTreble is the only supported clef.
	ClefTreble = "treble"
	// MaxMeasures caps a piece: the Music Region challenges the whole piece on
	// every difficulty, so a score longer than this would be an unfinishable
	// puzzle.
	MaxMeasures = 6
)

// Note is one sounding note inside a measure, in ticks. Equal Start values
// form a chord.
type Note struct {
	Start int `json:"start"`
	Dur   int `json:"dur"`
	Pitch int `json:"pitch"`
}

// Measure is one bar. ID is positional (m001, m002, ...) and never stored.
type Measure struct {
	ID    string `json:"-"`
	Len   int    `json:"len"`
	Notes []Note `json:"notes"`
}

// Piece is one score file. Axis is the load-time constant Axis and never
// appears in a file; Measures are stored in performance order.
type Piece struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Composer string    `json:"composer"`
	TempoBPM int       `json:"tempoBPM"`
	TimeSig  string    `json:"timeSig"`
	Key      string    `json:"key"`
	Clef     string    `json:"clef"`
	Axis     int       `json:"-"`
	Measures []Measure `json:"measures"`
}

// Parse decodes, canonicalises and validates one score file. id is the file
// base name and must match the piece id (mazes rule: a score is its file name).
// It enforces the MaxMeasures content cap, so a longer piece never ships.
func Parse(id string, raw []byte) (*Piece, error) {
	p, err := decode(raw, MaxMeasures)
	if err != nil {
		return nil, err
	}
	if p.ID != id {
		return nil, fmt.Errorf("score: id %q does not match file name %q", p.ID, id)
	}
	return p, nil
}

// ParseSnapshot decodes and canonicalises a frozen session snapshot. It applies
// every Parse rule except MaxMeasures: the cap gates new content, and a session
// created before the cap must keep loading with its longer frozen piece.
func ParseSnapshot(id string, raw []byte) (*Piece, error) {
	p, err := decode(raw, 0)
	if err != nil {
		return nil, err
	}
	if p.ID != id {
		return nil, fmt.Errorf("score: id %q does not match snapshot id %q", p.ID, id)
	}
	return p, nil
}

// decode parses a score without checking the file name. It enforces the fixed
// key set, assigns positional measure IDs, sorts notes into canonical order and
// validates the result. A maxMeasures of 0 skips the content cap and is only
// used for frozen session snapshots.
func decode(raw []byte, maxMeasures int) (*Piece, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Piece
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("score: decode: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("score: decode: trailing data after score object")
	}
	p.Axis = Axis
	for i := range p.Measures {
		p.Measures[i].ID = measureID(i)
		SortNotes(p.Measures[i].Notes)
	}
	if err := validate(&p, maxMeasures); err != nil {
		return nil, err
	}
	return &p, nil
}

// Encode renders the canonical file form: indented JSON, no axis, no measure
// IDs, canonical note order, trailing newline.
func Encode(p *Piece) ([]byte, error) {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("score: encode: %w", err)
	}
	return append(raw, '\n'), nil
}

// Validate rejects everything the game cannot render or solve, including a
// piece longer than MaxMeasures. Shipped and custom score files both go through
// it, so no file can encode a piece the Music Region refuses to challenge.
func Validate(p *Piece) error { return validate(p, MaxMeasures) }

// validate is Validate with a configurable content cap. A maxMeasures of 0
// skips the cap: only frozen session snapshots use that.
func validate(p *Piece, maxMeasures int) error {
	if p == nil {
		return errors.New("score: nil piece")
	}
	if p.Version != Version {
		return fmt.Errorf("score: version %d is not supported (want %d)", p.Version, Version)
	}
	if p.ID == "" {
		return errors.New("score: id is empty")
	}
	if p.TempoBPM <= 0 {
		return fmt.Errorf("score: tempoBPM %d must be positive", p.TempoBPM)
	}
	if p.Clef != ClefTreble {
		return fmt.Errorf("score: clef %q is not supported (only %q)", p.Clef, ClefTreble)
	}
	if len(p.Measures) == 0 {
		return errors.New("score: measures is empty")
	}
	if maxMeasures > 0 && len(p.Measures) > maxMeasures {
		return fmt.Errorf("score: %s: piece has %d measures, the Music Region caps pieces at %d", p.ID, len(p.Measures), maxMeasures)
	}
	for i, m := range p.Measures {
		where := measureLabel(i, m)
		if m.Len <= 0 {
			return fmt.Errorf("score: %s: len %d must be positive", where, m.Len)
		}
		if m.Len%tickGrid != 0 {
			return fmt.Errorf("score: %s: len %d is not on the 32nd-note grid (multiple of %d)", where, m.Len, tickGrid)
		}
		for j, n := range m.Notes {
			if n.Start < 0 {
				return fmt.Errorf("score: %s: note %d starts at %d", where, j, n.Start)
			}
			if n.Start%tickGrid != 0 {
				return fmt.Errorf("score: %s: note %d start %d is not on the 32nd-note grid (multiple of %d)", where, j, n.Start, tickGrid)
			}
			if n.Dur <= 0 {
				return fmt.Errorf("score: %s: note %d duration %d must be positive", where, j, n.Dur)
			}
			if !renderableDuration(n.Dur) {
				return fmt.Errorf("score: %s: note %d duration %d is not a renderable note value (triplets are not supported)", where, j, n.Dur)
			}
			if n.Start+n.Dur > m.Len {
				return fmt.Errorf("score: %s: note %d starts at %d for %d ticks, past the barline at %d (ties are not allowed)", where, j, n.Start, n.Dur, m.Len)
			}
			if n.Pitch < MinPitch || n.Pitch > MaxPitch {
				return fmt.Errorf("score: %s: note %d pitch %d outside [%d,%d] (|pitch-%d| <= 12)", where, j, n.Pitch, MinPitch, MaxPitch, Axis)
			}
		}
	}
	return nil
}

// tickGrid is the subdivision the engraver supports: every note head and every
// bar length lands on a 32nd-note grid.
const tickGrid = 60

// renderableDuration reports whether the command .5 engraver maps dur to a
// note value. PPQ 480: whole 1920, dotted half 1440, half 960, dotted quarter
// 720, quarter 480, dotted eighth 360, eighth 240, dotted 16th 180, 16th 120,
// 32nd 60. Tuplets have no glyph and are rejected.
func renderableDuration(dur int) bool {
	switch dur {
	case 1920, 1440, 960, 720, 480, 360, 240, 180, 120, 60:
		return true
	}
	return false
}

// SortNotes canonicalises note order: start, then pitch. The sort is stable so
// canonical input round-trips unchanged; equal start is a chord.
func SortNotes(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Start != notes[j].Start {
			return notes[i].Start < notes[j].Start
		}
		return notes[i].Pitch < notes[j].Pitch
	})
}

// Clone returns a deep copy, so a session can freeze a piece snapshot without
// aliasing the catalogue cache.
func (p *Piece) Clone() *Piece {
	if p == nil {
		return nil
	}
	out := *p
	out.Measures = make([]Measure, len(p.Measures))
	for i, m := range p.Measures {
		out.Measures[i] = m
		out.Measures[i].Notes = slices.Clone(m.Notes)
	}
	return &out
}

func measureID(i int) string { return fmt.Sprintf("m%03d", i+1) }

func measureLabel(i int, m Measure) string {
	if m.ID != "" {
		return "measure " + m.ID
	}
	return fmt.Sprintf("measure %d", i+1)
}

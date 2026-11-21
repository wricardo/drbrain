package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

const (
	// ppq is the ticks-per-quarter-note resolution used by every score.
	ppq = 480

	// Score pitches must sit inside [minPitch, maxPitch]: inversion mirrors
	// around B4 (MIDI 71) and the game keeps the melody within a twelfth.
	minPitch = 59
	maxPitch = 83

	// scoreVersion is the only supported score schema version.
	scoreVersion = 1
)

// Note is one sounding note. Start and Dur are ticks; Start+Dur must not
// exceed the containing measure's Len. Equal Start values form a chord.
type Note struct {
	Start int `json:"start"`
	Dur   int `json:"dur"`
	Pitch int `json:"pitch"`
}

// Measure is one bar. Len is identical for every measure of a piece.
type Measure struct {
	Len   int    `json:"len"`
	Notes []Note `json:"notes"`
}

// Piece is the on-disk score format read by games/music.
type Piece struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Composer string    `json:"composer"`
	TempoBPM int       `json:"tempoBPM"`
	TimeSig  string    `json:"timeSig"`
	Key      string    `json:"key"`
	Clef     string    `json:"clef"`
	Measures []Measure `json:"measures"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// measureLenForTimeSig returns the bar length in ticks. Beat values are
// counted at 480 ticks each, so 4/4 is 1920, 3/4 is 1440, 2/4 is 960,
// 6/8 is 2880, 9/8 is 4320 and 12/8 is 5760.
func measureLenForTimeSig(ts string) (int, error) {
	switch ts {
	case "4/4":
		return 4 * ppq, nil
	case "3/4":
		return 3 * ppq, nil
	case "2/4":
		return 2 * ppq, nil
	case "6/8":
		return 6 * ppq, nil
	case "9/8":
		return 9 * ppq, nil
	case "12/8":
		return 12 * ppq, nil
	}
	return 0, fmt.Errorf("unsupported time signature %q (want 4/4, 3/4, 2/4, 6/8, 9/8 or 12/8)", ts)
}

// sortNotes puts each measure into the canonical (Start, Pitch) order.
func (p *Piece) sortNotes() {
	for i := range p.Measures {
		notes := p.Measures[i].Notes
		sort.SliceStable(notes, func(a, b int) bool {
			if notes[a].Start != notes[b].Start {
				return notes[a].Start < notes[b].Start
			}
			return notes[a].Pitch < notes[b].Pitch
		})
		if notes == nil {
			notes = []Note{}
		}
		p.Measures[i].Notes = notes
	}
	if p.Measures == nil {
		p.Measures = []Measure{}
	}
}

// Validate enforces the score rules the game loader relies on.
func (p *Piece) Validate() error {
	if p.Version != scoreVersion {
		return fmt.Errorf("version %d is not supported (want %d)", p.Version, scoreVersion)
	}
	if !idPattern.MatchString(p.ID) {
		return fmt.Errorf("id %q is not lowercase kebab-case", p.ID)
	}
	if p.TempoBPM <= 0 {
		return fmt.Errorf("tempoBPM must be positive, got %d", p.TempoBPM)
	}
	if p.Clef != "treble" {
		return fmt.Errorf("unsupported clef %q (only treble)", p.Clef)
	}
	barLen, err := measureLenForTimeSig(p.TimeSig)
	if err != nil {
		return err
	}
	if len(p.Measures) == 0 {
		return fmt.Errorf("piece has no measures")
	}
	for mi := range p.Measures {
		m := &p.Measures[mi]
		if m.Len != barLen {
			return fmt.Errorf("measure %d: len %d does not match time signature %s (%d)", mi+1, m.Len, p.TimeSig, barLen)
		}
		if len(m.Notes) == 0 {
			return fmt.Errorf("measure %d: no notes (rests alone are not a measure)", mi+1)
		}
		for ni, n := range m.Notes {
			if n.Dur <= 0 {
				return fmt.Errorf("measure %d note %d: dur %d must be positive", mi+1, ni+1, n.Dur)
			}
			if n.Start < 0 {
				return fmt.Errorf("measure %d note %d: start %d is negative", mi+1, ni+1, n.Start)
			}
			if n.Start+n.Dur > m.Len {
				return fmt.Errorf("measure %d note %d: start %d + dur %d crosses the barline at %d", mi+1, ni+1, n.Start, n.Dur, m.Len)
			}
			if n.Pitch < minPitch || n.Pitch > maxPitch {
				return fmt.Errorf("measure %d note %d: pitch %d outside [%d,%d]", mi+1, ni+1, n.Pitch, minPitch, maxPitch)
			}
		}
	}
	return nil
}

// Encode sorts notes, validates the piece and renders deterministic JSON:
// two-space indent, struct field order, trailing newline.
func (p *Piece) Encode() ([]byte, error) {
	p.sortNotes()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

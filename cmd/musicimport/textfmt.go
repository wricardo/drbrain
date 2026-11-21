package main

import (
	"fmt"
	"strconv"
	"strings"
)

// source is a parsed melody before metadata and transposition are applied.
type source struct {
	timeSig  string
	key      string // only MusicXML fills this in
	measures []Measure
}

// parseText reads the simple tick notation:
//
//	<time signature> <token>...
//
// where a token is "PITCH:DUR", "P1+P2+...:DUR" (chord), "r:DUR" (rest) or
// "|" (barline). Durations are ticks at PPQ 480. Lines starting with '#' are
// comments.
func parseText(data []byte) (*source, error) {
	var tokens []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		tokens = append(tokens, strings.Fields(trimmed)...)
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("input is empty")
	}
	timeSig := tokens[0]
	barLen, err := measureLenForTimeSig(timeSig)
	if err != nil {
		return nil, err
	}
	src := &source{timeSig: timeSig}
	var notes []Note
	pos := 0
	measure := 1
	flush := func() error {
		if len(notes) == 0 {
			return fmt.Errorf("measure %d: bar has no notes (rests alone are not a measure)", measure)
		}
		src.measures = append(src.measures, Measure{Len: barLen, Notes: notes})
		notes = nil
		pos = 0
		measure++
		return nil
	}
	for _, tok := range tokens[1:] {
		if tok == "|" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		i := strings.LastIndexByte(tok, ':')
		if i <= 0 || i == len(tok)-1 {
			return nil, fmt.Errorf("measure %d: token %q is not PITCH:DUR", measure, tok)
		}
		head, tail := tok[:i], tok[i+1:]
		dur, err := strconv.Atoi(tail)
		if err != nil || dur <= 0 {
			return nil, fmt.Errorf("measure %d: duration %q must be a positive integer", measure, tail)
		}
		if head == "r" {
			pos += dur
			if pos > barLen {
				return nil, fmt.Errorf("measure %d: rest runs past the barline (%d > %d)", measure, pos, barLen)
			}
			continue
		}
		for _, name := range strings.Split(head, "+") {
			pitch, err := parsePitchName(name)
			if err != nil {
				return nil, fmt.Errorf("measure %d: %v", measure, err)
			}
			notes = append(notes, Note{Start: pos, Dur: dur, Pitch: pitch})
		}
		pos += dur
		if pos > barLen {
			return nil, fmt.Errorf("measure %d: notes run past the barline (%d > %d)", measure, pos, barLen)
		}
	}
	if len(notes) > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if len(src.measures) == 0 {
		return nil, fmt.Errorf("no measures in input")
	}
	return src, nil
}

// parsePitchName converts a scientific pitch name (C4, F#5, Bb3) to MIDI.
func parsePitchName(s string) (int, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("pitch %q is malformed", s)
	}
	step, ok := map[byte]int{'C': 0, 'D': 2, 'E': 4, 'F': 5, 'G': 7, 'A': 9, 'B': 11}[s[0]]
	if !ok {
		return 0, fmt.Errorf("pitch %q must start with A-G", s)
	}
	i := 1
	alter := 0
	for i < len(s) && (s[i] == '#' || s[i] == 'b') {
		if s[i] == '#' {
			alter++
		} else {
			alter--
		}
		i++
	}
	octave, err := strconv.Atoi(s[i:])
	if err != nil {
		return 0, fmt.Errorf("pitch %q has no octave", s)
	}
	midi := 12*(octave+1) + step + alter
	if midi < 0 || midi > 127 {
		return 0, fmt.Errorf("pitch %q is outside the MIDI range", s)
	}
	return midi, nil
}

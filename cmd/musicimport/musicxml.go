package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// The MusicXML subset this importer understands: one <part>, one voice, no
// <backup>/<forward>, treble clef, and ties that stay inside one measure.

type mxScore struct {
	XMLName xml.Name `xml:"score-partwise"`
	Parts   []mxPart `xml:"part"`
}

type mxPart struct {
	ID       string      `xml:"id,attr"`
	Measures []mxMeasure `xml:"measure"`
}

type mxMeasure struct {
	Number     string     `xml:"number,attr"`
	Attributes []mxAttrs  `xml:"attributes"`
	Notes      []mxNote   `xml:"note"`
	Backups    []struct{} `xml:"backup"`
	Forwards   []struct{} `xml:"forward"`
}

type mxAttrs struct {
	Divisions int     `xml:"divisions"`
	Key       *mxKey  `xml:"key"`
	Time      *mxTime `xml:"time"`
	Clef      *mxClef `xml:"clef"`
}

type mxKey struct {
	Fifths int    `xml:"fifths"`
	Mode   string `xml:"mode"`
}

type mxTime struct {
	Beats    string `xml:"beats"`
	BeatType int    `xml:"beat-type"`
}

type mxClef struct {
	Sign string `xml:"sign"`
}

type mxNote struct {
	Chord     []struct{}   `xml:"chord"`
	Rest      []struct{}   `xml:"rest"`
	Duration  int          `xml:"duration"`
	Voice     string       `xml:"voice"`
	Pitch     *mxPitch     `xml:"pitch"`
	Ties      []mxTie      `xml:"tie"`
	Notations *mxNotations `xml:"notations"`
}

type mxNotations struct {
	Tied []mxTie `xml:"tied"`
}

type mxPitch struct {
	Step   string `xml:"step"`
	Alter  int    `xml:"alter"`
	Octave int    `xml:"octave"`
}

type mxTie struct {
	Type string `xml:"type,attr"`
}

// parseMusicXML converts a score-partwise MusicXML document into a source
// melody, scaling each <duration> so one quarter note is 480 ticks.
func parseMusicXML(data []byte) (*source, error) {
	var doc mxScore
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse MusicXML: %w", err)
	}
	if len(doc.Parts) == 0 {
		return nil, fmt.Errorf("MusicXML has no <part>")
	}
	if len(doc.Parts) > 1 {
		return nil, fmt.Errorf("MusicXML has %d <part> elements (polyphony unsupported)", len(doc.Parts))
	}

	part := doc.Parts[0]
	divisions := 0
	beats := 0
	beatType := 0
	timeSig := ""
	key := ""
	src := &source{}

	pendingIdx := -1
	pendingMeasureIdx := -1
	pendingMeasure := ""

	for mi := range part.Measures {
		m := &part.Measures[mi]
		number := m.Number
		if number == "" {
			number = strconv.Itoa(mi + 1)
		}
		if pendingMeasureIdx >= 0 && pendingMeasureIdx != mi {
			return nil, fmt.Errorf("measure %s: tie crosses barline (started in measure %s)", number, pendingMeasure)
		}
		if len(m.Backups) > 0 || len(m.Forwards) > 0 {
			return nil, fmt.Errorf("measure %s: <backup>/<forward> (multiple voices) unsupported", number)
		}
		for _, a := range m.Attributes {
			if a.Divisions > 0 {
				divisions = a.Divisions
			}
			if a.Time != nil {
				b, err := parseBeats(a.Time.Beats)
				if err != nil {
					return nil, fmt.Errorf("measure %s: %v", number, err)
				}
				ts := fmt.Sprintf("%d/%d", b, a.Time.BeatType)
				if _, err := measureLenForTimeSig(ts); err != nil {
					return nil, fmt.Errorf("measure %s: %v", number, err)
				}
				if timeSig == "" {
					timeSig, beats, beatType = ts, b, a.Time.BeatType
				} else if ts != timeSig {
					return nil, fmt.Errorf("measure %s: time signature changes from %s to %s (unsupported)", number, timeSig, ts)
				}
			}
			if a.Clef != nil {
				sign := strings.ToUpper(strings.TrimSpace(a.Clef.Sign))
				if sign != "" && sign != "G" {
					return nil, fmt.Errorf("measure %s: unsupported clef %q (only treble G)", number, a.Clef.Sign)
				}
			}
			if a.Key != nil && key == "" {
				key = keyName(a.Key.Fifths, a.Key.Mode)
			}
		}
		if timeSig == "" {
			return nil, fmt.Errorf("measure %s: missing <time> in <attributes>", number)
		}
		if divisions <= 0 {
			return nil, fmt.Errorf("measure %s: missing <divisions> in <attributes>", number)
		}

		voices := map[string]bool{}
		for _, n := range m.Notes {
			if v := strings.TrimSpace(n.Voice); v != "" {
				voices[v] = true
			}
		}
		if len(voices) > 1 {
			return nil, fmt.Errorf("measure %s: multiple voices (polyphony unsupported)", number)
		}

		barLen, _ := measureLenForTimeSig(timeSig)
		measure := Measure{Len: barLen}
		pos := 0
		prevStart := 0
		for ni := range m.Notes {
			n := &m.Notes[ni]
			if n.Duration <= 0 {
				return nil, fmt.Errorf("measure %s: note %d has no positive <duration>", number, ni+1)
			}
			dur, err := scaleDuration(n.Duration, divisions, beats, beatType)
			if err != nil {
				return nil, fmt.Errorf("measure %s: %v", number, err)
			}
			if len(n.Rest) > 0 {
				if pendingIdx >= 0 {
					return nil, fmt.Errorf("measure %s: tie crosses barline (rest before tie stop)", number)
				}
				pos += dur
				if pos > barLen {
					return nil, fmt.Errorf("measure %s: rest runs past the barline", number)
				}
				continue
			}
			if n.Pitch == nil {
				return nil, fmt.Errorf("measure %s: note %d has neither <pitch> nor <rest>", number, ni+1)
			}
			start := pos
			if len(n.Chord) > 0 {
				start = prevStart
			} else {
				prevStart = start
			}
			pitch := midiFromPitch(n.Pitch)
			stopTie, startTie := tieDirection(n)

			if stopTie {
				if pendingIdx < 0 {
					return nil, fmt.Errorf("measure %s: tie stop without tie start", number)
				}
				target := &measure.Notes[pendingIdx]
				if target.Pitch != pitch {
					return nil, fmt.Errorf("measure %s: tied notes differ in pitch", number)
				}
				target.Dur += dur
				if target.Start+target.Dur > barLen {
					return nil, fmt.Errorf("measure %s: tie crosses barline", number)
				}
				if !startTie {
					pendingIdx = -1
					pendingMeasureIdx = -1
					pendingMeasure = ""
				}
			} else {
				if pendingIdx >= 0 {
					return nil, fmt.Errorf("measure %s: tie is not resolved by the next note", number)
				}
				measure.Notes = append(measure.Notes, Note{Start: start, Dur: dur, Pitch: pitch})
				if startTie {
					pendingIdx = len(measure.Notes) - 1
					pendingMeasureIdx = mi
					pendingMeasure = number
				}
			}

			pos = start + dur
			if pos > barLen {
				return nil, fmt.Errorf("measure %s: note extends past the barline", number)
			}
		}
		src.measures = append(src.measures, measure)
	}
	if pendingIdx >= 0 {
		last := part.Measures[len(part.Measures)-1].Number
		if last == "" {
			last = strconv.Itoa(len(part.Measures))
		}
		return nil, fmt.Errorf("measure %s: tie crosses barline (no tie stop after the barline)", last)
	}

	if len(src.measures) == 0 {
		return nil, fmt.Errorf("MusicXML has no measures")
	}
	src.timeSig = timeSig
	src.key = key
	return src, nil
}

// parseBeats accepts plain values and composite beat counts such as "3+2".
func parseBeats(s string) (int, error) {
	s = strings.TrimSpace(s)
	total := 0
	for _, part := range strings.Split(s, "+") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("invalid <beats> %q", s)
		}
		total += v
	}
	if total == 0 {
		return 0, fmt.Errorf("invalid <beats> %q", s)
	}
	return total, nil
}

// scaleDuration converts a MusicXML duration (in divisions per quarter) into
// ticks, using the time signature to map the bar onto measureLenForTimeSig.
func scaleDuration(dur, divisions, beats, beatType int) (int, error) {
	// A bar of beats/beatType lasts beats*4/beatType quarter notes, i.e.
	// divisions*beats*4/beatType division units; it must map onto
	// beats*480 ticks.
	denom := 4 * divisions
	if denom <= 0 {
		return 0, fmt.Errorf("invalid <divisions> %d", divisions)
	}
	num := dur * ppq * beatType
	if num%denom != 0 {
		return 0, fmt.Errorf("duration %d (divisions %d) is not representable at PPQ %d", dur, divisions, ppq)
	}
	return num / denom, nil
}

// midiFromPitch converts a MusicXML pitch element to MIDI. MusicXML octaves
// are scientific pitch notation, so C4 is middle C (60).
func midiFromPitch(p *mxPitch) int {
	step := map[string]int{"C": 0, "D": 2, "E": 4, "F": 5, "G": 7, "A": 9, "B": 11}[strings.ToUpper(strings.TrimSpace(p.Step))]
	return 12*(p.Octave+1) + step + p.Alter
}

// tieDirection reports whether a note is tied from before and/or tied on.
func tieDirection(n *mxNote) (stop, start bool) {
	visit := func(t mxTie) {
		switch strings.ToLower(strings.TrimSpace(t.Type)) {
		case "stop":
			stop = true
		case "start":
			start = true
		case "continue":
			stop, start = true, true
		}
	}
	for _, t := range n.Ties {
		visit(t)
	}
	if n.Notations != nil {
		for _, t := range n.Notations.Tied {
			visit(t)
		}
	}
	return stop, start
}

// keyName turns a MusicXML fifths count into a key name such as "D" or "Am".
func keyName(fifths int, mode string) string {
	major := map[int]string{
		-7: "Cb", -6: "Gb", -5: "Db", -4: "Ab", -3: "Eb", -2: "Bb", -1: "F",
		0: "C", 1: "G", 2: "D", 3: "A", 4: "E", 5: "B", 6: "F#", 7: "C#",
	}
	minor := map[int]string{
		-7: "Abm", -6: "Ebm", -5: "Bbm", -4: "Fm", -3: "Cm", -2: "Gm", -1: "Dm",
		0: "Am", 1: "Em", 2: "Bm", 3: "F#m", 4: "C#m", 5: "G#m", 6: "D#m", 7: "A#m",
	}
	if strings.EqualFold(strings.TrimSpace(mode), "minor") {
		if name, ok := minor[fifths]; ok {
			return name
		}
	}
	if name, ok := major[fifths]; ok {
		return name
	}
	return "C"
}

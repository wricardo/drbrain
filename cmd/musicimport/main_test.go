package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTextImport_ByteDeterministic(t *testing.T) {
	src, err := os.ReadFile("testdata/text-input.music")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/text-input.json")
	if err != nil {
		t.Fatal(err)
	}
	m := meta{
		id:       "text-input",
		title:    "Text Input",
		composer: "Test Composer",
		key:      "D",
		clef:     "treble",
		tempo:    100,
	}
	first, err := convert(src, "text-input.music", m)
	if err != nil {
		t.Fatalf("first convert: %v", err)
	}
	second, err := convert(src, "text-input.music", m)
	if err != nil {
		t.Fatalf("second convert: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two imports of the same input produced different bytes")
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("output does not match testdata/text-input.json\n--- got ---\n%s\n--- want ---\n%s", first, want)
	}
	if !bytes.HasSuffix(first, []byte("}\n")) {
		t.Fatal("output must end with a trailing newline")
	}
}

func TestMusicXML_DivisionsNotPPQ(t *testing.T) {
	src, err := os.ReadFile("testdata/divisions-2.musicxml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := convert(src, "divisions-2.musicxml", meta{id: "divisions-two", title: "Divisions", composer: "Test", tempo: 100})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var p Piece
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.TimeSig != "4/4" || p.Key != "D" || p.Clef != "treble" {
		t.Fatalf("metadata = %q/%q/%q, want 4/4/D/treble", p.TimeSig, p.Key, p.Clef)
	}
	if len(p.Measures) != 2 {
		t.Fatalf("got %d measures, want 2", len(p.Measures))
	}
	want := []struct {
		start, dur, pitch int
	}{
		{0, 480, 62},
		{480, 480, 66},
		{960, 960, 69},
	}
	for i, w := range want {
		n := p.Measures[0].Notes[i]
		if n.Start != w.start || n.Dur != w.dur || n.Pitch != w.pitch {
			t.Errorf("measure 1 note %d = %+v, want start=%d dur=%d pitch=%d", i+1, n, w.start, w.dur, w.pitch)
		}
	}
	if n := p.Measures[1].Notes[0]; n.Start != 0 || n.Dur != 1920 || n.Pitch != 69 {
		t.Errorf("measure 2 note = %+v, want whole-note A4", n)
	}
}

func TestMusicXML_CrossBarlineTieRejected(t *testing.T) {
	src, err := os.ReadFile("testdata/crossbar-tie.musicxml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = convert(src, "crossbar-tie.musicxml", meta{id: "crossbar-tie", title: "Tie", composer: "Test", tempo: 100})
	if err == nil {
		t.Fatal("cross-barline tie was accepted")
	}
	if !strings.Contains(err.Error(), "measure 2") {
		t.Fatalf("error %q does not name the measure", err)
	}
	if !strings.Contains(err.Error(), "tie crosses barline") {
		t.Fatalf("error %q does not explain the tie problem", err)
	}
}

func TestTimeSignatures(t *testing.T) {
	want := map[string]int{
		"4/4":  1920,
		"3/4":  1440,
		"2/4":  960,
		"6/8":  2880,
		"9/8":  4320,
		"12/8": 5760,
	}
	for ts, ticks := range want {
		got, err := measureLenForTimeSig(ts)
		if err != nil {
			t.Errorf("measureLenForTimeSig(%q): %v", ts, err)
			continue
		}
		if got != ticks {
			t.Errorf("measureLenForTimeSig(%q) = %d, want %d", ts, got, ticks)
		}
	}
	if _, err := measureLenForTimeSig("5/4"); err == nil {
		t.Fatal("5/4 was accepted")
	}
}

func TestTextImport_NineEight(t *testing.T) {
	src := []byte("9/8 B3:480 D4:480 B3:480 F#4:480 G4:480 A4:480 B4:1440 |\n")
	got, err := convert(src, "nine-eight.music", meta{id: "nine-eight", title: "9/8", composer: "Test", tempo: 96})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var p Piece
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.TimeSig != "9/8" || len(p.Measures) != 1 || p.Measures[0].Len != 4320 {
		t.Fatalf("got %s / %d measures of %d ticks, want 9/8 / 1 measure of 4320", p.TimeSig, len(p.Measures), p.Measures[0].Len)
	}
	notes := p.Measures[0].Notes
	if len(notes) != 7 {
		t.Fatalf("got %d notes, want 7", len(notes))
	}
	if last := notes[6]; last.Start != 2880 || last.Dur != 1440 || last.Pitch != 71 {
		t.Fatalf("last note = %+v, want start 2880 dur 1440 pitch 71", last)
	}
}

func TestTransposeOutOfRangeRejected(t *testing.T) {
	src := []byte("4/4 D4:480 D4:480 F#4:480 A4:480 |\n")
	_, err := convert(src, "range.music", meta{id: "range", title: "Range", composer: "Test", tempo: 100, transpose: -20})
	if err == nil {
		t.Fatal("out-of-range transpose was accepted")
	}
	if !strings.Contains(err.Error(), "outside [59,83]") {
		t.Fatalf("error %q does not mention the pitch range", err)
	}
}

func TestTextImport_RejectsUnsupportedTimeSignature(t *testing.T) {
	_, err := convert([]byte("5/4 C4:480 |"), "odd.music", meta{id: "odd", title: "Odd", composer: "Test", tempo: 100})
	if err == nil || !strings.Contains(err.Error(), "unsupported time signature") {
		t.Fatalf("error = %v, want unsupported time signature", err)
	}
}

func TestTextImport_RejectsRestsOnlyMeasure(t *testing.T) {
	_, err := convert([]byte("4/4 C4:480 | r:1920 |\n"), "rests.music", meta{id: "rests", title: "Rests", composer: "Test", tempo: 100})
	if err == nil || !strings.Contains(err.Error(), "measure 2") {
		t.Fatalf("error = %v, want a measure 2 error", err)
	}
}

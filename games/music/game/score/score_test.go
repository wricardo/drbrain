package score

import (
	"reflect"
	"strings"
	"testing"
)

const validScoreJSON = `{
  "version": 1,
  "id": "twinkle",
  "title": "Twinkle, Twinkle",
  "composer": "Traditional",
  "tempoBPM": 96,
  "timeSig": "4/4",
  "key": "C major",
  "clef": "treble",
  "measures": [
    {
      "len": 480,
      "notes": [
        {"start": 240, "dur": 120, "pitch": 67},
        {"start": 0, "dur": 240, "pitch": 60},
        {"start": 240, "dur": 120, "pitch": 64}
      ]
    },
    {"len": 480, "notes": [{"start": 0, "dur": 480, "pitch": 62}]}
  ]
}`

func mustParse(t *testing.T, id, raw string) *Piece {
	t.Helper()
	p, err := Parse(id, []byte(raw))
	if err != nil {
		t.Fatalf("Parse(%q): %v", id, err)
	}
	return p
}

func TestParse_ValidPieceCanonicalizes(t *testing.T) {
	p := mustParse(t, "twinkle", validScoreJSON)

	if p.Axis != Axis {
		t.Fatalf("axis = %d, want %d", p.Axis, Axis)
	}
	if p.Version != Version || p.ID != "twinkle" {
		t.Fatalf("version/id = %d/%q", p.Version, p.ID)
	}
	if len(p.Measures) != 2 {
		t.Fatalf("measures = %d, want 2", len(p.Measures))
	}
	if p.Measures[0].ID != "m001" || p.Measures[1].ID != "m002" {
		t.Fatalf("measure ids = %q/%q", p.Measures[0].ID, p.Measures[1].ID)
	}
	want := []Note{
		{Start: 0, Dur: 240, Pitch: 60},
		{Start: 240, Dur: 120, Pitch: 64},
		{Start: 240, Dur: 120, Pitch: 67},
	}
	if !reflect.DeepEqual(p.Measures[0].Notes, want) {
		t.Fatalf("notes = %+v, want %+v", p.Measures[0].Notes, want)
	}
}

func TestParse_RejectsUnknownField(t *testing.T) {
	cases := map[string]string{
		"axis in file": strings.Replace(validScoreJSON, `"clef": "treble",`, `"clef": "treble", "axis": 71,`, 1),
		"measure id":   strings.Replace(validScoreJSON, `"len": 480,`, `"len": 480, "id": "m001",`, 1),
		"note field":   strings.Replace(validScoreJSON, `"pitch": 60`, `"pitch": 60, "velocity": 1`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("twinkle", []byte(raw)); err == nil {
				t.Fatal("Parse accepted an unknown field")
			}
		})
	}
}

func TestParse_RejectsTrailingData(t *testing.T) {
	if _, err := Parse("twinkle", []byte(validScoreJSON+"\n{}")); err == nil {
		t.Fatal("Parse accepted trailing JSON")
	}
}

func TestParse_RejectsIDMismatch(t *testing.T) {
	_, err := Parse("other", []byte(validScoreJSON))
	if err == nil {
		t.Fatal("Parse accepted a file name that does not match the piece id")
	}
	if !strings.Contains(err.Error(), "twinkle") || !strings.Contains(err.Error(), "other") {
		t.Fatalf("error %q should name both ids", err)
	}
}

func TestValidate_RejectsInvalidPiece(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Piece)
	}{
		{"version", func(p *Piece) { p.Version = 2 }},
		{"empty id", func(p *Piece) { p.ID = "" }},
		{"no measures", func(p *Piece) { p.Measures = nil }},
		{"measure len", func(p *Piece) { p.Measures[0].Len = 0 }},
		{"note start", func(p *Piece) { p.Measures[0].Notes[0].Start = -1 }},
		{"note duration", func(p *Piece) { p.Measures[0].Notes[0].Dur = 0 }},
		{"note past barline", func(p *Piece) { p.Measures[0].Notes[0].Start = 360 }},
		{"start off the 32nd grid", func(p *Piece) { p.Measures[0].Notes[0].Start = 30 }},
		{"triplet duration", func(p *Piece) { p.Measures[0].Notes[0].Dur = 160 }},
		{"unrenderable duration", func(p *Piece) { p.Measures[0].Notes[0].Dur = 100 }},
		{"measure len off the 32nd grid", func(p *Piece) { p.Measures[0].Len = 470 }},
		{"pitch below axis range", func(p *Piece) { p.Measures[0].Notes[0].Pitch = MinPitch - 1 }},
		{"pitch above axis range", func(p *Piece) { p.Measures[0].Notes[0].Pitch = MaxPitch + 1 }},
		{"unsupported clef", func(p *Piece) { p.Clef = "bass" }},
		{"tempo", func(p *Piece) { p.TempoBPM = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParse(t, "twinkle", validScoreJSON)
			tc.mutate(p)
			if err := Validate(p); err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
		})
	}

	if err := Validate(nil); err == nil {
		t.Fatal("Validate(nil) = nil")
	}
}

func TestValidate_RejectsTripletWithRenderableMessage(t *testing.T) {
	p := mustParse(t, "twinkle", validScoreJSON)
	p.Measures[0].Notes[0].Dur = 160
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted a triplet duration")
	}
	msg := err.Error()
	for _, want := range []string{"note 0", "160", "not a renderable note value", "triplets"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}

func TestValidate_RejectsEveryNonRenderableDuration(t *testing.T) {
	allowed := map[int]bool{1920: true, 1440: true, 960: true, 720: true, 480: true, 360: true, 240: true, 180: true, 120: true, 60: true}
	for dur := 1; dur <= 2000; dur++ {
		p := mustParse(t, "twinkle", validScoreJSON)
		p.Measures[0].Notes = nil
		p.Measures[0].Len = 1920
		p.Measures[0].Notes = []Note{{Start: 0, Dur: dur, Pitch: Axis}}
		err := Validate(p)
		if allowed[dur] && err != nil {
			t.Fatalf("Validate rejected renderable duration %d: %v", dur, err)
		}
		if !allowed[dur] && err == nil {
			t.Fatalf("Validate accepted unrenderable duration %d", dur)
		}
	}
}

func TestValidate_AcceptBoundaries(t *testing.T) {
	p := mustParse(t, "twinkle", validScoreJSON)
	p.Measures[0].Notes = []Note{
		{Start: 0, Dur: 480, Pitch: MinPitch},
		{Start: 0, Dur: 120, Pitch: MaxPitch},
	}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEncode_RoundTripIsLossless(t *testing.T) {
	p := mustParse(t, "twinkle", validScoreJSON)

	raw, err := Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(string(raw), `"axis"`) {
		t.Fatalf("encoded score leaks the axis: %s", raw)
	}
	if strings.Contains(string(raw), `"m001"`) {
		t.Fatalf("encoded score leaks measure ids: %s", raw)
	}

	again, err := Parse("twinkle", raw)
	if err != nil {
		t.Fatalf("Parse(Encode): %v", err)
	}
	if !reflect.DeepEqual(p, again) {
		t.Fatalf("round trip changed the piece:\n got %+v\nwant %+v", again, p)
	}
}

func TestClone_DeepCopiesMeasures(t *testing.T) {
	p := mustParse(t, "twinkle", validScoreJSON)
	clone := p.Clone()
	clone.Measures[0].Notes[0].Pitch = 59
	if p.Measures[0].Notes[0].Pitch == 59 {
		t.Fatal("Clone aliased the original notes")
	}
}

func TestValidate_RejectsPieceOverTheCap(t *testing.T) {
	if MaxMeasures != 6 {
		t.Fatalf("MaxMeasures = %d, want the Music Region cap of 6", MaxMeasures)
	}
	p := mustParse(t, "twinkle", validScoreJSON)
	for len(p.Measures) <= MaxMeasures {
		p.Measures = append(p.Measures, p.Measures[0])
	}
	err := Validate(p)
	if err == nil {
		t.Fatalf("Validate accepted %d measures", len(p.Measures))
	}
	msg := err.Error()
	for _, want := range []string{"twinkle", "7", "Music Region", "6"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}

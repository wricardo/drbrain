package engine

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/wricardo/drbrain/games/music/game/score"
)

func testMeasure() score.Measure {
	return score.Measure{
		ID:  "m001",
		Len: 480,
		Notes: []score.Note{
			{Start: 0, Dur: 120, Pitch: 60},
			{Start: 240, Dur: 240, Pitch: 64},
			{Start: 240, Dur: 60, Pitch: 71},
		},
	}
}

func recreate(m score.Measure) score.Measure {
	out := m
	out.Notes = append([]score.Note(nil), m.Notes...)
	return out
}

func TestRetrograde_Involution(t *testing.T) {
	m := testMeasure()
	got := Retrograde(m)
	want := []score.Note{
		{Start: 0, Dur: 240, Pitch: 64},
		{Start: 180, Dur: 60, Pitch: 71},
		{Start: 360, Dur: 120, Pitch: 60},
	}
	if !reflect.DeepEqual(got.Notes, want) {
		t.Fatalf("Retrograde notes = %+v, want %+v", got.Notes, want)
	}
	if got.Len != m.Len || len(got.Notes) != len(m.Notes) {
		t.Fatalf("Retrograde changed len/count: %d/%d", got.Len, len(got.Notes))
	}
	if twice := Retrograde(got); !reflect.DeepEqual(twice, m) {
		t.Fatalf("Retrograde twice = %+v, want %+v", twice, m)
	}
	if m.ID != got.ID {
		t.Fatalf("Retrograde changed the measure id: %q", got.ID)
	}
}

func TestRetrograde_DoesNotAliasNotes(t *testing.T) {
	m := testMeasure()
	got := Retrograde(m)
	got.Notes[0].Pitch = 59
	if m.Notes[0].Pitch == 59 {
		t.Fatal("Retrograde aliased the source notes")
	}
}

func TestInvert_Involution(t *testing.T) {
	m := testMeasure()
	got := Invert(m, score.Axis)
	if got.Len != m.Len || len(got.Notes) != len(m.Notes) {
		t.Fatalf("Invert changed len/count: %d/%d", got.Len, len(got.Notes))
	}
	for _, n := range got.Notes {
		if n.Pitch < score.MinPitch || n.Pitch > score.MaxPitch {
			t.Fatalf("pitch %d escaped [%d,%d]", n.Pitch, score.MinPitch, score.MaxPitch)
		}
	}
	if twice := Invert(got, score.Axis); !reflect.DeepEqual(twice, m) {
		t.Fatalf("Invert twice = %+v, want %+v", twice, m)
	}
}

func TestApply_ComposesAndIsInvolution(t *testing.T) {
	m := testMeasure()
	transforms := []Transform{
		{},
		{FlipH: true},
		{FlipV: true},
		{FlipH: true, FlipV: true},
	}
	for _, tr := range transforms {
		got := Apply(recreate(m), tr)
		if twice := Apply(got, tr); !reflect.DeepEqual(twice, m) {
			t.Fatalf("Apply(%+v) twice = %+v, want %+v", tr, twice, m)
		}
	}
	if got := Apply(recreate(m), Transform{}); !reflect.DeepEqual(got, m) {
		t.Fatalf("Apply(no flips) changed the measure: %+v", got)
	}
	if got, want := Apply(recreate(m), Transform{FlipH: true}), Retrograde(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply(FlipH) = %+v, want %+v", got, want)
	}
	if got, want := Apply(recreate(m), Transform{FlipV: true}), Invert(m, score.Axis); !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply(FlipV) = %+v, want %+v", got, want)
	}
}

// distinctPiece builds n bars that all differ, inside the pitch range.
func distinctPiece(n int) *score.Piece {
	measures := make([]score.Measure, n)
	for i := range measures {
		measures[i] = score.Measure{
			ID:  fmt.Sprintf("m%03d", i+1),
			Len: 480,
			Notes: []score.Note{
				{Start: 0, Dur: 120, Pitch: 60 + i%12},
				{Start: 120, Dur: 120, Pitch: 72 + i%5},
				{Start: 240, Dur: 180, Pitch: 66 + i%9},
			},
		}
	}
	return &score.Piece{
		Version:  score.Version,
		ID:       "test",
		Title:    "Test Piece",
		Composer: "Anonymous",
		TempoBPM: 80,
		TimeSig:  "4/4",
		Key:      "C major",
		Clef:     score.ClefTreble,
		Axis:     score.Axis,
		Measures: measures,
	}
}

func TestDifficulty_ParseAndReward(t *testing.T) {
	cases := []struct {
		d       Difficulty
		name    string
		percent int
	}{
		{Novice, "novice", 5},
		{Expert, "expert", 10},
		{Genius, "genius", 15},
	}
	for _, tc := range cases {
		if tc.d.String() != tc.name {
			t.Fatalf("String() = %q, want %q", tc.d, tc.name)
		}
		if got := tc.d.PercentPerWin(); got != tc.percent {
			t.Fatalf("%s.PercentPerWin() = %d, want %d", tc.name, got, tc.percent)
		}
		parsed, err := ParseDifficulty("  " + tc.name + " ")
		if err != nil || parsed != tc.d {
			t.Fatalf("ParseDifficulty(%q) = %v, %v", tc.name, parsed, err)
		}
	}
	if _, err := ParseDifficulty("impossible"); err == nil {
		t.Fatal("ParseDifficulty accepted an unknown name")
	}
}

func TestGenerate_UsesWholePiece(t *testing.T) {
	cases := []struct {
		measures int
		d        Difficulty
	}{
		{40, Novice},
		{40, Expert},
		{40, Genius},
		{20, Novice},
		{20, Expert},
		{20, Genius},
		{6, Novice},
		{6, Expert},
		{6, Genius},
	}
	for _, tc := range cases {
		puzzle, err := Generate(distinctPiece(tc.measures), tc.d, 7)
		if err != nil {
			t.Fatalf("Generate(%d measures, %s): %v", tc.measures, tc.d, err)
		}
		if len(puzzle.Slots) != tc.measures {
			t.Fatalf("Generate(%d measures, %s) slots = %d, want all %d bars", tc.measures, tc.d, len(puzzle.Slots), tc.measures)
		}
	}
}

func TestGenerate_TooShortIsErrNoPuzzle(t *testing.T) {
	for _, d := range []Difficulty{Novice, Expert, Genius} {
		if _, err := Generate(distinctPiece(1), d, 1); !errors.Is(err, ErrNoPuzzle) {
			t.Fatalf("Generate(1, %s) = %v, want ErrNoPuzzle", d, err)
		}
	}
	if _, err := Generate(nil, Novice, 1); !errors.Is(err, ErrNoPuzzle) {
		t.Fatalf("Generate(nil) = %v, want ErrNoPuzzle", err)
	}
	if _, err := Generate(distinctPiece(8), Difficulty(0), 1); !errors.Is(err, ErrNoPuzzle) {
		t.Fatalf("Generate(unknown difficulty) = %v, want ErrNoPuzzle", err)
	}
}

func TestGenerate_NeverStartsSolved(t *testing.T) {
	piece := distinctPiece(24)
	for _, d := range []Difficulty{Novice, Expert, Genius} {
		for seed := int64(1); seed <= 20; seed++ {
			puzzle, err := Generate(piece, d, seed)
			if err != nil {
				t.Fatalf("Generate(%s, seed %d): %v", d, seed, err)
			}
			if puzzle.Solved() {
				t.Fatalf("Generate(%s, seed %d) returned a solved puzzle", d, seed)
			}
		}
	}
}

func TestGenerate_HonorsRequiredFlips(t *testing.T) {
	piece := distinctPiece(24)
	for seed := int64(1); seed <= 20; seed++ {
		novice, err := Generate(piece, Novice, seed)
		if err != nil {
			t.Fatalf("Generate(Novice, seed %d): %v", seed, err)
		}
		for i, slot := range novice.Slots {
			if slot.Transform != (Transform{}) {
				t.Fatalf("Novice slot %d has flips %+v", i, slot.Transform)
			}
		}

		expert, err := Generate(piece, Expert, seed)
		if err != nil {
			t.Fatalf("Generate(Expert, seed %d): %v", seed, err)
		}
		var h int
		for _, slot := range expert.Slots {
			if slot.Transform.FlipV {
				t.Fatalf("Expert seed %d set a vertical flip: %+v", seed, slot.Transform)
			}
			if slot.Transform.FlipH {
				h++
			}
		}
		if want := (len(expert.Slots) + 2) / 3; h < want {
			t.Fatalf("Expert seed %d has %d horizontal flips, want at least %d", seed, h, want)
		}

		genius, err := Generate(piece, Genius, seed)
		if err != nil {
			t.Fatalf("Generate(Genius, seed %d): %v", seed, err)
		}
		var anyH, anyV bool
		mixed, n := 0, len(genius.Slots)
		for _, slot := range genius.Slots {
			anyH = anyH || slot.Transform.FlipH
			anyV = anyV || slot.Transform.FlipV
			if slot.Transform.FlipH && slot.Transform.FlipV {
				mixed++
			}
		}
		if !anyH || !anyV {
			t.Fatalf("Genius seed %d flips h=%v v=%v", seed, anyH, anyV)
		}
		if 2*mixed < n {
			t.Fatalf("Genius seed %d mixed %d of %d", seed, mixed, n)
		}
	}
}

func TestGenerate_PieceWithoutDistinctBarsIsErrNoPuzzle(t *testing.T) {
	piece := distinctPiece(4)
	for i := range piece.Measures {
		piece.Measures[i].Notes = []score.Note{{Start: 0, Dur: 120, Pitch: 60}}
	}
	if _, err := Generate(piece, Novice, 5); !errors.Is(err, ErrNoPuzzle) {
		t.Fatalf("Generate(identical bars) = %v, want ErrNoPuzzle", err)
	}
}

func TestGenerate_DeterministicAndPure(t *testing.T) {
	piece := distinctPiece(20)
	before := piece.Clone()

	first, err := Generate(piece, Expert, 42)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate(piece, Expert, 42)
	if err != nil {
		t.Fatalf("Generate again: %v", err)
	}
	if !reflect.DeepEqual(first.Slots, second.Slots) || first.Seed != second.Seed || first.Difficulty != second.Difficulty {
		t.Fatalf("same seed produced different puzzles:\n%+v\n%+v", first.Slots, second.Slots)
	}
	if !reflect.DeepEqual(piece, before) {
		t.Fatal("Generate mutated the piece")
	}
}

func TestGenerate_SlotsReferenceWholePiece(t *testing.T) {
	piece := distinctPiece(20)
	for _, d := range []Difficulty{Novice, Expert, Genius} {
		puzzle, err := Generate(piece, d, 3)
		if err != nil {
			t.Fatalf("Generate(%s): %v", d, err)
		}
		whole := make(map[string]bool, len(piece.Measures))
		for _, m := range piece.Measures {
			whole[m.ID] = true
		}
		seen := make(map[string]bool, len(piece.Measures))
		for i, slot := range puzzle.Slots {
			if !whole[slot.MeasureID] {
				t.Fatalf("%s slot %d uses %q outside the piece", d, i, slot.MeasureID)
			}
			if seen[slot.MeasureID] {
				t.Fatalf("%s slot %d repeats %q", d, i, slot.MeasureID)
			}
			seen[slot.MeasureID] = true
		}
		if len(seen) != len(piece.Measures) {
			t.Fatalf("%s covers %d of %d bars", d, len(seen), len(piece.Measures))
		}
	}
}

func solvedPiece() *score.Piece {
	a := score.Measure{
		ID:  "m001",
		Len: 480,
		Notes: []score.Note{
			{Start: 0, Dur: 120, Pitch: 60},
			{Start: 240, Dur: 120, Pitch: 64},
		},
	}
	duplicate := a
	duplicate.ID = "m002"
	b := score.Measure{
		ID:  "m003",
		Len: 480,
		Notes: []score.Note{
			{Start: 120, Dur: 240, Pitch: 67},
		},
	}
	return &score.Piece{
		Version:  score.Version,
		ID:       "dup",
		Title:    "Duplicates",
		Composer: "Anonymous",
		TempoBPM: 80,
		TimeSig:  "4/4",
		Key:      "C major",
		Clef:     score.ClefTreble,
		Axis:     score.Axis,
		Measures: []score.Measure{a, duplicate, b},
	}
}

func targetPuzzle() *Puzzle {
	piece := solvedPiece()
	return &Puzzle{
		Piece: piece,
		Slots: []Placement{
			{MeasureID: piece.Measures[0].ID},
			{MeasureID: piece.Measures[1].ID},
			{MeasureID: piece.Measures[2].ID},
		},
		Difficulty: Novice,
	}
}

func TestSolved_DuplicateMeasuresAreInterchangeable(t *testing.T) {
	puzzle := targetPuzzle()
	if !puzzle.Solved() {
		t.Fatal("target order is not solved")
	}
	if err := puzzle.Swap(0, 1); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if !puzzle.Solved() {
		t.Fatal("swapping identical bars broke the solved check")
	}
	if err := puzzle.Swap(1, 2); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if puzzle.Solved() {
		t.Fatal("swapping a distinct bar still reported solved")
	}
}

func TestSolved_FlipBreaksMatch(t *testing.T) {
	puzzle := targetPuzzle()
	if err := puzzle.SetTransform(0, Transform{FlipH: true}); err != nil {
		t.Fatalf("SetTransform: %v", err)
	}
	if puzzle.Solved() {
		t.Fatal("a retrograde bar still reported solved")
	}
	if err := puzzle.Swap(0, 1); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if puzzle.Solved() {
		t.Fatal("moving the flipped duplicate still reported solved")
	}
}

func TestSolved_RejectsUnknownMeasure(t *testing.T) {
	puzzle := targetPuzzle()
	puzzle.Slots[0].MeasureID = "nope"
	if puzzle.Solved() {
		t.Fatal("unknown measure reported solved")
	}
}

func TestSolved_UsesSlotMeasureLengths(t *testing.T) {
	piece := &score.Piece{
		Version: score.Version,
		ID:      "lengths",
		Axis:    score.Axis,
		Measures: []score.Measure{
			{ID: "m001", Len: 480, Notes: []score.Note{{Start: 0, Dur: 120, Pitch: 60}}},
			{ID: "m002", Len: 240, Notes: []score.Note{{Start: 0, Dur: 120, Pitch: 60}}},
		},
	}
	puzzle := &Puzzle{
		Piece: piece,
		Slots: []Placement{{MeasureID: "m001"}, {MeasureID: "m002"}},
	}
	if !puzzle.Solved() {
		t.Fatal("target order is not solved")
	}
	if err := puzzle.Swap(0, 1); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	// Offsets must follow the slot's own measure length (240 before 480), not
	// the target order's.
	if puzzle.Solved() {
		t.Fatal("swapped bars of different lengths reported solved")
	}
}

func TestSolved_RequiresWholePiece(t *testing.T) {
	piece := distinctPiece(20)
	slots := make([]Placement, 8)
	for i := range slots {
		slots[i] = Placement{MeasureID: piece.Measures[i].ID}
	}
	puzzle := &Puzzle{Piece: piece, Slots: slots}
	if puzzle.Solved() {
		t.Fatal("a prefix arrangement of 8 of 20 bars reported solved")
	}
}

func TestSolved_WholePieceManyBars(t *testing.T) {
	for _, n := range []int{16, 21} {
		piece := distinctPiece(n)
		slots := make([]Placement, n)
		for i := range slots {
			slots[i] = Placement{MeasureID: piece.Measures[i].ID}
		}
		puzzle := &Puzzle{Piece: piece, Slots: slots}
		if !puzzle.Solved() {
			t.Fatalf("%d-bar target order is not solved", n)
		}
		if err := puzzle.Swap(0, n-1); err != nil {
			t.Fatalf("%d-bar Swap: %v", n, err)
		}
		if puzzle.Solved() {
			t.Fatalf("%d-bar swapped arrangement reported solved", n)
		}
	}
}

func TestPuzzle_MutationsAreBoundsCheckedAndPreservePiece(t *testing.T) {
	piece := solvedPiece()
	before := piece.Clone()
	puzzle := &Puzzle{
		Piece: piece,
		Slots: []Placement{{MeasureID: "m001"}, {MeasureID: "m002"}, {MeasureID: "m003"}},
	}
	if err := puzzle.Swap(-1, 0); !errors.Is(err, ErrIndex) {
		t.Fatalf("Swap(-1,0) = %v", err)
	}
	if err := puzzle.Swap(0, 3); !errors.Is(err, ErrIndex) {
		t.Fatalf("Swap(0,3) = %v", err)
	}
	if err := puzzle.SetTransform(3, Transform{}); !errors.Is(err, ErrIndex) {
		t.Fatalf("SetTransform(3) = %v", err)
	}
	if err := puzzle.Swap(0, 2); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if err := puzzle.SetTransform(1, Transform{FlipH: true}); err != nil {
		t.Fatalf("SetTransform: %v", err)
	}
	if !reflect.DeepEqual(piece, before) {
		t.Fatal("puzzle mutations changed the shared piece")
	}
}

func TestPuzzle_ResetRestoresScramble(t *testing.T) {
	puzzle, err := Generate(distinctPiece(12), Expert, 9)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	start := append([]Placement(nil), puzzle.Slots...)

	if err := puzzle.Swap(0, 1); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if err := puzzle.SetTransform(2, Transform{FlipH: true}); err != nil {
		t.Fatalf("SetTransform: %v", err)
	}
	if reflect.DeepEqual(puzzle.Slots, start) {
		t.Fatal("mutations did not change the puzzle")
	}

	puzzle.Reset()
	if !reflect.DeepEqual(puzzle.Slots, start) {
		t.Fatalf("Reset left %+v, want %+v", puzzle.Slots, start)
	}
}

package session

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func testPiece() *score.Piece {
	measures := make([]score.Measure, 4)
	for i := range measures {
		measures[i] = score.Measure{
			Len:   480,
			Notes: []score.Note{{Start: 0, Dur: 120, Pitch: 60 + i}},
		}
	}
	return &score.Piece{
		Version: score.Version, ID: "test", Title: "Test", Composer: "Anonymous",
		TempoBPM: 80, TimeSig: "4/4", Key: "C major", Clef: score.ClefTreble,
		Measures: measures,
	}
}

func arrangement() []engine.Placement {
	return []engine.Placement{
		{MeasureID: "m001", Transform: engine.Transform{FlipH: true}},
		{MeasureID: "m002"},
		{MeasureID: "m003"},
		{MeasureID: "m004"},
	}
}

func testSession() *service.Session {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	return &service.Session{
		ID: "0123456789abcdef", DisplayName: "Ada", Difficulty: engine.Expert, Seed: 42,
		Piece: testPiece(), Slots: arrangement(), Seq: 3,
		CreatedAt: now, LastActionAt: now.Add(time.Minute), Wins: 1, Solved: false,
	}
}

func TestStorePersistence_RoundTrip(t *testing.T) {
	sp, err := NewPostgresPersistence(pgtest.Pool(t), 0)
	if err != nil {
		t.Fatalf("NewPostgresPersistence: %v", err)
	}
	saved := testSession()
	if err := sp.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := sp.Load(saved.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DisplayName != saved.DisplayName || loaded.Seed != saved.Seed ||
		loaded.Difficulty != engine.Expert || loaded.Seq != saved.Seq ||
		loaded.Wins != saved.Wins || loaded.Solved != saved.Solved {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Piece == nil || loaded.Piece.ID != "test" || len(loaded.Piece.Measures) != 4 {
		t.Fatalf("piece = %+v", loaded.Piece)
	}
	if loaded.Piece.Axis != score.Axis || loaded.Piece.Measures[0].ID != "m001" {
		t.Fatalf("piece was not canonicalised: axis=%d id=%q", loaded.Piece.Axis, loaded.Piece.Measures[0].ID)
	}
	if !reflect.DeepEqual(loaded.Slots, saved.Slots) {
		t.Fatalf("slots = %+v, want %+v", loaded.Slots, saved.Slots)
	}
	if !loaded.CreatedAt.Equal(saved.CreatedAt) || !loaded.LastActionAt.Equal(saved.LastActionAt) {
		t.Fatalf("times = %v/%v", loaded.CreatedAt, loaded.LastActionAt)
	}
}

func TestLoad_RejectsSlotWithMissingMeasure(t *testing.T) {
	sp := NewMemoryPersistence(0)
	s := testSession()
	s.Slots[2].MeasureID = "nope"
	if err := sp.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := sp.Load(s.ID)
	if err == nil || !strings.Contains(err.Error(), "not in the piece") {
		t.Fatalf("Load = %v, want a missing-measure error", err)
	}
}

func TestLoad_RejectsSolvedFlagMismatch(t *testing.T) {
	sp := NewMemoryPersistence(0)
	s := testSession()
	s.Solved = true // the flipped arrangement is not solved
	if err := sp.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := sp.Load(s.ID)
	if err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("Load = %v, want a solved flag mismatch", err)
	}
}

// longPiece builds n distinct bars with positional ids, like a loaded score.
func longPiece(n int) *score.Piece {
	measures := make([]score.Measure, n)
	for i := range measures {
		measures[i] = score.Measure{
			ID:    fmt.Sprintf("m%03d", i+1),
			Len:   480,
			Notes: []score.Note{{Start: 0, Dur: 120, Pitch: 60 + i%12}, {Start: 240, Dur: 120, Pitch: 72 + i%5}},
		}
	}
	p := testPiece()
	p.Measures = measures
	return p
}

// TestLoad_RepairsLegacyWindowArrangement covers a session payload written under
// the old per-difficulty window rule: 8 slots for a piece whose snapshot has 18
// bars. Load must not trust or keep that short arrangement; it rebuilds the
// deterministic whole-piece scramble and preserves identity and counters. The
// 18-bar snapshot also predates the 6-bar content cap, and must still load: the
// cap gates new content, not a player's frozen session.
func TestLoad_RepairsLegacyWindowArrangement(t *testing.T) {
	sp := NewMemoryPersistence(0)
	piece := longPiece(18)
	legacy := make([]engine.Placement, 8)
	for i := range legacy {
		legacy[i] = engine.Placement{MeasureID: fmt.Sprintf("m%03d", i+1)}
	}
	legacy[0].Transform = engine.Transform{FlipH: true}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s := &service.Session{
		ID: "0123456789abcdef", DisplayName: "Legacy", Difficulty: engine.Expert, Seed: 42,
		Piece: piece, Slots: legacy, Seq: 5, CreatedAt: now, LastActionAt: now, Wins: 2,
	}
	if err := sp.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := sp.Load(s.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Slots) != 18 {
		t.Fatalf("slots = %d, want all 18 bars", len(loaded.Slots))
	}
	if loaded.Solved {
		t.Fatal("repaired legacy arrangement is solved")
	}
	if loaded.Wins != 2 || loaded.Seq != 5 || loaded.Difficulty != engine.Expert {
		t.Fatalf("repaired session = %+v", loaded)
	}
	canonical, err := canonicalPiece(piece)
	if err != nil {
		t.Fatalf("canonicalPiece: %v", err)
	}
	want, err := engine.Generate(canonical, engine.Expert, s.Seed)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !reflect.DeepEqual(loaded.Slots, want.Slots) {
		t.Fatalf("repaired slots = %+v, want %+v", loaded.Slots, want.Slots)
	}
}

package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/games/music/game/score"
)

// TestShippedLibrary_GeneratesEveryDifficulty guards the public-domain score
// library as it lands: at least 8 pieces, each within the Music Region cap of
// score.MaxMeasures bars, and every difficulty builds a whole-piece,
// well-formed, not-already-solved puzzle from it. Every difficulty must use
// all the bars. Short, duplicate-heavy phrases are the risk here, so the test
// sweeps 200 seeds per difficulty: Generate must never exhaust its retry loop.
// It skips until the library directory exists; it never imports the ingest
// command.
func TestShippedLibrary_GeneratesEveryDifficulty(t *testing.T) {
	dir := filepath.Join("..", "..", "scores")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Skipf("shipped scores not present at %s", dir)
	}

	catalogue, err := score.NewCatalogue(dir, t.TempDir())
	if err != nil {
		t.Fatalf("NewCatalogue(%s): %v", dir, err)
	}
	entries, err := catalogue.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) < 8 {
		t.Fatalf("library has %d pieces, want at least 8", len(entries))
	}

	for _, entry := range entries {
		n := len(entry.Piece.Measures)
		if n < 2 || n > score.MaxMeasures {
			t.Errorf("%s: %d measures, want 2..%d", entry.ID, n, score.MaxMeasures)
			continue
		}
		for _, d := range []Difficulty{Novice, Expert, Genius} {
			for seed := int64(1); seed <= 200; seed++ {
				puzzle, err := Generate(entry.Piece, d, seed)
				if err != nil {
					t.Fatalf("%s/%s seed %d: %v", entry.ID, d, seed, err)
				}
				if len(puzzle.Slots) != n {
					t.Fatalf("%s/%s seed %d: %d slots, want all %d bars", entry.ID, d, seed, len(puzzle.Slots), n)
				}
				if puzzle.Solved() {
					t.Fatalf("%s/%s seed %d: generated an already-solved puzzle", entry.ID, d, seed)
				}
				flips := make([]Transform, len(puzzle.Slots))
				for i, slot := range puzzle.Slots {
					flips[i] = slot.Transform
				}
				if !hasRequiredFlips(d, flips) {
					t.Fatalf("%s/%s seed %d: flips %+v do not carry the difficulty guarantee", entry.ID, d, seed, flips)
				}
			}
		}
	}
}

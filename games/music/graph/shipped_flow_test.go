package graph_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/games/music/game/service"
	"github.com/wricardo/drbrain/games/music/game/session"
	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	"github.com/wricardo/drbrain/games/music/transport/websocket"
	rootgraph "github.com/wricardo/drbrain/graph"
)

type measureJSON struct {
	ID    string     `json:"id"`
	Len   int        `json:"len"`
	Notes []noteJSON `json:"notes"`
}

type noteJSON struct {
	At    int `json:"at"`
	Dur   int `json:"dur"`
	Pitch int `json:"pitch"`
}

type excerptJSON struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Composer string        `json:"composer"`
	TempoBpm int           `json:"tempoBPM"`
	Measures []measureJSON `json:"measures"`
	Notes    []noteJSON    `json:"notes"`
}

func newServerForScores(t *testing.T, scoresDir string) *httptest.Server {
	t.Helper()
	catalogue, err := score.NewCatalogue(scoresDir, t.TempDir())
	if err != nil {
		t.Fatalf("NewCatalogue(%s): %v", scoresDir, err)
	}
	persistence, err := session.NewStorePersistence(t.TempDir(), 100)
	if err != nil {
		t.Fatalf("NewStorePersistence: %v", err)
	}
	manager := session.NewManager(persistence, 100)
	if err := manager.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	hub := websocket.NewHub()
	svc := service.New(catalogue, manager, hub, service.Options{})
	resolver := musicgraph.NewResolver(svc, hub)
	handler := rootgraph.NewHandler(rootgraph.Options{
		Resolver:      rootgraph.New(nil, nil, nil, nil, nil, resolver),
		Introspection: true,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// TestShippedScores_CreateAndSolveAllDifficulties is the API-level smoke for
// the real library: every difficulty creates a whole-piece session on a
// shipped excerpt, honours its flip rules, and can be solved by a
// transform-aware measure matching.
func TestShippedScores_CreateAndSolveAllDifficulties(t *testing.T) {
	scoresDir := filepath.Join("..", "scores")
	if info, err := os.Stat(scoresDir); err != nil || !info.IsDir() {
		t.Skipf("shipped scores not present at %s", scoresDir)
	}
	ts := newServerForScores(t, scoresDir)

	var excerpts []excerptJSON
	operation(t, post(t, ts, `{ music { excerpts { id title composer tempoBPM measures { id len notes { at dur pitch } } notes { at dur pitch } } } }`, nil), "excerpts", &excerpts)
	if len(excerpts) != 8 {
		t.Fatalf("excerpts = %d, want 8", len(excerpts))
	}

	excerpt := excerpts[0]
	if len(excerpt.Measures) < 2 || len(excerpt.Measures) > score.MaxMeasures {
		t.Fatalf("%s has %d measures, want 2..%d", excerpt.ID, len(excerpt.Measures), score.MaxMeasures)
	}
	if len(excerpt.Measures[0].Notes) == 0 || len(excerpt.Notes) == 0 {
		t.Fatalf("%s exposes no notes", excerpt.ID)
	}

	for _, tc := range []struct {
		difficulty string
		percent    int
	}{
		{"NOVICE", 5},
		{"EXPERT", 10},
		{"GENIUS", 15},
	} {
		t.Run(tc.difficulty, func(t *testing.T) {
			created := mustSession(t, post(t, ts,
				`mutation($id: ID!, $d: Music_Difficulty!, $seed: Int) { music { createSession(excerptID: $id, difficulty: $d, displayName: "Smoke", seed: $seed) { `+sessionFields+` } } }`,
				map[string]any{"id": excerpt.ID, "d": tc.difficulty, "seed": int64(11)}), "createSession")
			n := len(created.Slots)
			if n != len(excerpt.Measures) {
				t.Fatalf("slots = %d, want the whole piece (%d bars)", n, len(excerpt.Measures))
			}
			if created.PercentPerWin != tc.percent {
				t.Fatalf("percentPerWin = %d, want %d", created.PercentPerWin, tc.percent)
			}
			if created.Solved {
				t.Fatal("created session is already solved")
			}
			assertFlips(t, tc.difficulty, created.Slots)

			whole := make(map[string]bool, n)
			for _, m := range excerpt.Measures {
				whole[m.ID] = true
			}
			for _, slot := range created.Slots {
				if !whole[slot.MeasureID] {
					t.Fatalf("slot %d uses %q outside the piece", slot.Position, slot.MeasureID)
				}
			}

			slots := solvePiece(t, excerpt.Measures)
			solved := mustSession(t, post(t, ts,
				`mutation($id: ID!, $slots: [Music_PlacementInput!]!) { music { setArrangement(sessionID: $id, slots: $slots) { `+sessionFields+` } } }`,
				map[string]any{"id": created.ID, "slots": slots}), "setArrangement")
			if !solved.Solved || solved.Wins != 1 {
				t.Fatalf("solved = %v, wins = %d", solved.Solved, solved.Wins)
			}
			if solved.LastChange == nil || solved.LastChange.Kind != service.KindSolve {
				t.Fatalf("last change = %+v", solved.LastChange)
			}

			// Query it back to prove the mutation and the read path agree.
			queried := mustSession(t, post(t, ts,
				`query($id: ID!) { music { session(id: $id) { `+sessionFields+` } } }`,
				map[string]any{"id": created.ID}), "session")
			if !queried.Solved || queried.Wins != 1 || queried.Seq != solved.Seq {
				t.Fatalf("queried = %+v", queried)
			}
		})
	}
}

func assertFlips(t *testing.T, difficulty string, slots []slotJSON) {
	t.Helper()
	var anyH, anyV bool
	h, mixed := 0, 0
	for _, s := range slots {
		anyH = anyH || s.FlipH
		anyV = anyV || s.FlipV
		if s.FlipH {
			h++
		}
		if s.FlipH && s.FlipV {
			mixed++
		}
	}
	switch difficulty {
	case "NOVICE":
		if anyH || anyV {
			t.Fatalf("novice slots carry flips: %+v", slots)
		}
	case "EXPERT":
		if anyV {
			t.Fatalf("expert slots carry a vertical flip: %+v", slots)
		}
		if want := (len(slots) + 2) / 3; h < want {
			t.Fatalf("expert has %d horizontal flips of %d, want at least %d", h, len(slots), want)
		}
	case "GENIUS":
		if !anyH || !anyV {
			t.Fatalf("genius flips h=%v v=%v", anyH, anyV)
		}
		if 2*mixed < len(slots) {
			t.Fatalf("genius mixed = %d of %d", mixed, len(slots))
		}
	}
}

type noteKey struct{ at, dur, pitch int }

type flipPair struct{ h, v bool }

func transformedCounts(m measureJSON, f flipPair) map[noteKey]int {
	out := make(map[noteKey]int, len(m.Notes))
	for _, n := range m.Notes {
		at, pitch := n.At, n.Pitch
		if f.h {
			at = m.Len - at - n.Dur
		}
		if f.v {
			pitch = 2*71 - pitch
		}
		out[noteKey{at, n.Dur, pitch}]++
	}
	return out
}

func sameCounts(a, b map[noteKey]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, want := range b {
		if a[k] != want {
			return false
		}
	}
	return true
}

type placementChoice struct {
	measure int
	flip    flipPair
}

// solvePiece builds a SetArrangement payload that reproduces the whole-piece
// target stream: target slot i is measure i (no flip); any piece measure under
// one of the four transforms that yields the same (len, note multiset) may fill
// it. Matching is bipartite (augmenting paths), so repeated bars cannot
// dead-end the greedy order.
func solvePiece(t *testing.T, piece []measureJSON) []map[string]any {
	t.Helper()
	n := len(piece)
	candidates := make([][]placementChoice, n)
	for i := range piece {
		want := transformedCounts(piece[i], flipPair{})
		for j := range piece {
			if piece[j].Len != piece[i].Len {
				continue
			}
			for _, f := range []flipPair{{false, false}, {true, false}, {false, true}, {true, true}} {
				if sameCounts(transformedCounts(piece[j], f), want) {
					candidates[i] = append(candidates[i], placementChoice{measure: j, flip: f})
					break
				}
			}
		}
	}

	matchMeasure := make([]int, n)
	for i := range matchMeasure {
		matchMeasure[i] = -1
	}
	matchOfSlot := make([]placementChoice, n)
	var assign func(slot int, seen []bool) bool
	assign = func(slot int, seen []bool) bool {
		for _, cand := range candidates[slot] {
			if seen[cand.measure] {
				continue
			}
			seen[cand.measure] = true
			if matchMeasure[cand.measure] == -1 || assign(matchMeasure[cand.measure], seen) {
				matchMeasure[cand.measure] = slot
				matchOfSlot[slot] = cand
				return true
			}
		}
		return false
	}
	for slot := range n {
		if !assign(slot, make([]bool, n)) {
			t.Fatalf("no unused measure reproduces target slot %d", slot)
		}
	}

	slots := make([]map[string]any, n)
	for i, cand := range matchOfSlot {
		slots[i] = map[string]any{
			"measureID": piece[cand.measure].ID,
			"flipH":     cand.flip.h,
			"flipV":     cand.flip.v,
		}
	}
	return slots
}

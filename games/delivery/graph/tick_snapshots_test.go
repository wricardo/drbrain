package graph

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// newSnapshotServer wires a manager with the tick log enabled and returns the
// live session so a test can advance the world directly.
func newSnapshotServer(t *testing.T) (*httptest.Server, *session.Session) {
	t.Helper()
	pool := pgtest.Pool(t)
	cat := newCityCatalog(t, pool)
	mgr, err := session.NewManagerWithOptions(cat, pool, session.ManagerOptions{TickSnapshots: 50})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	sess, _, err := mgr.Create("snap-gql", 3, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Do(func(w *engine.World) {
		for range 5 {
			w.Advance()
		}
	})

	ts := httptest.NewServer(testHandler(mgr, cat))
	t.Cleanup(ts.Close)
	return ts, sess
}

// tickSnapshotJSON is the wire shape of one frame.
type tickSnapshotJSON struct {
	SessionID  string
	Tick       int
	RecordedAt time.Time
	Money      int
	Reputation float64
	Cars       []struct {
		ID      string
		X       int
		Y       int
		Battery int
	}
	Orders []struct {
		ID    string
		State string
	}
}

const tickSnapshotsQuery = `query ($sessionId: ID!, $fromTick: Int, $toTick: Int, $afterTick: Int, $limit: Int) {
	tickSnapshots(sessionId: $sessionId, fromTick: $fromTick, toTick: $toTick, afterTick: $afterTick, limit: $limit) {
		sessionId
		tick
		recordedAt
		money
		reputation
		cars { id x y battery }
		orders { id state }
	}
}`

func readSnapshots(t *testing.T, ts *httptest.Server, vars map[string]any) []tickSnapshotJSON {
	t.Helper()
	res := query(t, ts, tickSnapshotsQuery, vars)
	if len(res.Errors) > 0 {
		t.Fatalf("tickSnapshots: %v", res.Errors)
	}
	var out struct{ TickSnapshots []tickSnapshotJSON }
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatal(err)
	}
	return out.TickSnapshots
}

func snapshotTicks(rows []tickSnapshotJSON) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.Tick
	}
	return out
}

// TestTickSnapshotsQuery covers the read path end to end: the writer is
// asynchronous, so the test polls until the frames land, then checks filtering,
// keyset paging and the frame's shape.
func TestTickSnapshotsQuery(t *testing.T) {
	ts, sess := newSnapshotServer(t)
	vars := map[string]any{"sessionId": sess.ID}

	var rows []tickSnapshotJSON
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows = readSnapshots(t, ts, vars)
		if len(rows) == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := snapshotTicks(rows); len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Fatalf("ticks = %v, want [1 2 3 4 5]", got)
	}
	first := rows[0]
	if first.SessionID != sess.ID || first.RecordedAt.IsZero() {
		t.Fatalf("frame identity = %+v", first)
	}
	if len(first.Cars) == 0 {
		t.Fatal("frame has no cars")
	}
	if first.Cars[0].ID == "" || first.Cars[0].Battery < 0 {
		t.Fatalf("car = %+v", first.Cars[0])
	}

	// Inclusive tick window.
	rows = readSnapshots(t, ts, map[string]any{"sessionId": sess.ID, "fromTick": 2, "toTick": 4})
	if got := snapshotTicks(rows); len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Fatalf("window ticks = %v, want [2 3 4]", got)
	}

	// Keyset paging: afterTick is exclusive and combined with limit.
	rows = readSnapshots(t, ts, map[string]any{"sessionId": sess.ID, "afterTick": 3, "limit": 2})
	if got := snapshotTicks(rows); len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("page ticks = %v, want [4 5]", got)
	}

	// An unknown session is NOT_FOUND, not an empty page.
	res := query(t, ts, tickSnapshotsQuery, map[string]any{"sessionId": "nope"})
	if len(res.Errors) == 0 {
		t.Fatal("unknown session returned no error")
	}

	// Invalid arguments are rejected instead of silently clamped.
	for _, vars := range []map[string]any{
		{"sessionId": sess.ID, "limit": 0},
		{"sessionId": sess.ID, "fromTick": 5, "toTick": 2},
		{"sessionId": sess.ID, "fromTick": -1},
	} {
		res := query(t, ts, tickSnapshotsQuery, vars)
		if len(res.Errors) == 0 {
			t.Fatalf("args %v were accepted", vars)
		}
	}
}

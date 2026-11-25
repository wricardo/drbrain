package session

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// newSnapshotsManager builds a manager over a fresh database schema with the
// tick log enabled.
func newSnapshotsManager(t *testing.T, retention int) *Manager {
	t.Helper()
	mgr, err := NewManagerWithOptions(newTestCatalog(t), pgtest.Pool(t), ManagerOptions{
		TickSnapshots: retention,
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func advance(t *testing.T, s *Session, ticks int) {
	t.Helper()
	s.Do(func(w *engine.World) {
		for range ticks {
			w.Advance()
		}
	})
}

func ticksOf(snaps []TickSnapshot) []int64 {
	out := make([]int64, len(snaps))
	for i, s := range snaps {
		out[i] = s.Tick
	}
	return out
}

// TestTickSnapshotsRecordEachTick covers the write path: every completed tick,
// including the ticks inside a single multi-tick Do, produces one frame.
func TestTickSnapshotsRecordEachTick(t *testing.T) {
	mgr := newSnapshotsManager(t, 100)
	s, _, err := mgr.Create("snap-1", 7, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	advance(t, s, 3)

	var snaps []TickSnapshot
	waitFor(t, func() bool {
		var err error
		snaps, err = mgr.TickSnapshots(context.Background(), s.ID, TickSnapshotQuery{})
		return err == nil && len(snaps) == 3
	})
	if got := ticksOf(snaps); got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("ticks = %v, want [1 2 3]", got)
	}
	first := snaps[0].State
	if first.Tick != 1 {
		t.Fatalf("frame tick = %d, want 1", first.Tick)
	}
	if len(first.Cars) == 0 {
		t.Fatal("frame has no cars")
	}
	if first.Ledger == nil {
		t.Fatal("frame has no ledger")
	}
	// The append-only histories and event feed belong to their own projections,
	// not to every frame.
	if first.Ledger.Cash != nil || first.Ledger.ReputationEntries != nil {
		t.Fatal("frame copied the ledger history")
	}
	if first.Events != nil {
		t.Fatal("frame copied the event feed")
	}
}

// TestTickSnapshotsRetainNewestTicks covers the retention bound: a session
// never keeps more than the configured number of frames.
func TestTickSnapshotsRetainNewestTicks(t *testing.T) {
	mgr := newSnapshotsManager(t, 3)
	s, _, err := mgr.Create("snap-retain", 7, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	advance(t, s, 8)

	var snaps []TickSnapshot
	waitFor(t, func() bool {
		var err error
		snaps, err = mgr.TickSnapshots(context.Background(), s.ID, TickSnapshotQuery{})
		return err == nil && len(snaps) == 3 && snaps[2].Tick == 8
	})
	if got := ticksOf(snaps); got[0] != 6 || got[1] != 7 || got[2] != 8 {
		t.Fatalf("ticks = %v, want [6 7 8]", got)
	}
}

// TestTickSnapshotsPageAndFilter covers the reader's window and keyset paging
// contract: inclusive bounds, an exclusive afterTick, and a normalized limit.
func TestTickSnapshotsPageAndFilter(t *testing.T) {
	mgr := newSnapshotsManager(t, 100)
	s, _, err := mgr.Create("snap-page", 7, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	advance(t, s, 10)
	waitFor(t, func() bool {
		rows, err := mgr.TickSnapshots(context.Background(), s.ID, TickSnapshotQuery{})
		return err == nil && len(rows) == 10
	})

	ctx := context.Background()
	cases := []struct {
		name  string
		query TickSnapshotQuery
		want  []int64
	}{
		{"window", TickSnapshotQuery{FromTick: 3, ToTick: 6}, []int64{3, 4, 5, 6}},
		{"window+limit", TickSnapshotQuery{FromTick: 3, ToTick: 6, Limit: 2}, []int64{3, 4}},
		{"after", TickSnapshotQuery{AfterTick: 4, Limit: 2}, []int64{5, 6}},
		{"after last", TickSnapshotQuery{AfterTick: 8}, []int64{9, 10}},
		{"after beats from", TickSnapshotQuery{FromTick: 2, AfterTick: 7, Limit: 2}, []int64{8, 9}},
		{"limit capped", TickSnapshotQuery{Limit: maxTickSnapshotLimit + 100}, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	}
	for _, tc := range cases {
		rows, err := mgr.TickSnapshots(ctx, s.ID, tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := ticksOf(rows)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: ticks = %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: ticks = %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

// TestDeleteRemovesTickSnapshots covers cleanup: deleting the save must not
// leave frames behind, even when some were still queued when the delete ran.
func TestDeleteRemovesTickSnapshots(t *testing.T) {
	mgr := newSnapshotsManager(t, 100)
	s, _, err := mgr.Create("snap-del", 7, "", "city")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	advance(t, s, 2)
	if err := mgr.Delete(s.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	snaps, err := mgr.TickSnapshots(context.Background(), s.ID, TickSnapshotQuery{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("frames after delete = %v, want none", ticksOf(snaps))
	}
}

// TestTickSnapshotsDisabled confirms a manager built without options does not
// record and reports the feature as off rather than empty.
func TestTickSnapshotsDisabled(t *testing.T) {
	mgr := newTestManager(t)
	if _, err := mgr.TickSnapshots(context.Background(), "any", TickSnapshotQuery{}); err != ErrSnapshotsDisabled {
		t.Fatalf("err = %v, want ErrSnapshotsDisabled", err)
	}
}

// TestExportFrameDropsHistory is the engine-side guarantee the storage saving
// depends on: the full save keeps the statement, the frame does not.
func TestExportFrameDropsHistory(t *testing.T) {
	city, cfg := testCity(t)
	w, err := engine.NewWorldSeeded(city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	w.Ledger.Cash = []engine.CashEntry{{Seq: 0, Kind: engine.CashOpening, Amount: 100, Balance: 100}}
	w.Ledger.ReputationEntries = []engine.ReputationEntry{{Seq: 0, Reason: engine.ReputationOpening, Balance: 1}}
	w.Events = []engine.Event{{Tick: 1, Type: engine.EventBlocked, Message: "blocked"}}

	full := w.ExportState()
	frame := w.ExportFrame()
	if len(full.Ledger.Cash) != 1 || len(full.Ledger.ReputationEntries) != 1 {
		t.Fatal("full export lost its ledger history")
	}
	if frame.Ledger.Cash != nil || frame.Ledger.ReputationEntries != nil || frame.Events != nil {
		t.Fatal("frame kept history")
	}
	if frame.Ledger.Money != full.Ledger.Money || frame.Tick != full.Tick {
		t.Fatal("frame lost non-history state")
	}
}
